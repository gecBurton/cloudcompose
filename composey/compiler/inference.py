import hashlib
import json
from typing import Optional

from composey.models.aws import (
    ALB_DATA_SOURCE_KEY,
    CLOUDFRONT_PROVIDER_REF,
    AppAutoscalingPolicy,
    AppAutoscalingTarget,
    AWSResources,
    CloudfrontDistribution,
    CloudwatchEventRule,
    CloudwatchEventTarget,
    CloudWatchLogGroup,
    ContainerDefinition,
    DbInstance,
    DbSubnetGroup,
    DockerImage,
    DockerRegistryImage,
    EcrRepository,
    EcsService,
    EcsTaskDefinition,
    ElastiCacheCluster,
    ElastiCacheSubnetGroup,
    IamRole,
    IamRolePolicy,
    LbListenerRule,
    LbTargetGroup,
    RandomPassword,
    S3Bucket,
    SecretsManagerSecret,
    SecretsManagerSecretVersion,
    SecurityGroup,
    SecurityGroupRule,
    TerraformLifecycle,
    Wafv2WebAcl,
)
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application as SemanticApp
from composey.models.semantic import Connection, CronSchedule, Schedule
from composey.models.semantic import Service as SemanticService

from .connections import resolve_environment


def _eventbridge_expression(schedule: Schedule) -> str:
    """
    Render a cloud-neutral schedule as an EventBridge schedule expression.

    EventBridge cron takes six fields rather than the standard five (it adds a
    year), and requires exactly one of day-of-month and day-of-week to be the
    '?' placeholder rather than '*'.
    """
    if not isinstance(schedule, CronSchedule):
        # rate(1 hour) is singular, rate(2 hours) is plural.
        unit = schedule.unit if schedule.value != 1 else schedule.unit.rstrip("s")
        return f"rate({schedule.value} {unit})"

    minute, hour, day_of_month, month, day_of_week = schedule.expression.split()

    if day_of_week == "*":
        day_of_week = "?"
    elif day_of_month == "*":
        day_of_month = "?"
    else:
        raise ValueError(
            f"schedule {schedule.expression!r} constrains both day-of-month and "
            f"day-of-week, which EventBridge cannot express: it requires one of "
            f"them to be unset."
        )

    return f"cron({minute} {hour} {day_of_month} {month} {day_of_week} *)"


# Ports the managed services listen on once substituted. Held here rather than
# rediscovered at each use, so environment wiring and security group rules
# cannot disagree about how a client reaches a service.
_DATABASE_PORTS = {"postgres": 5432, "mysql": 3306, "mariadb": 3306}
_CACHE_PORT = 6379


def _database_engine(image: str) -> str:
    lowered = image.lower()
    if "mysql" in lowered:
        return "mysql"
    if "mariadb" in lowered:
        return "mariadb"
    return "postgres"


def _connection_for(service: SemanticService) -> Optional[Connection]:
    """
    Describe how a client reaches this service once AWS has replaced it.

    Returns None for anything composey runs as a container, which clients reach
    without the compiler rewriting anything.
    """
    if service.capability == "database":
        return Connection(
            host=f"${{aws_db_instance.{service.name}_db.address}}",
            port=_DATABASE_PORTS[_database_engine(service.image)],
        )

    if service.capability == "cache":
        return Connection(
            host=f"${{aws_elasticache_cluster.{service.name}_cache.cache_nodes[0].address}}",
            port=_CACHE_PORT,
        )

    if service.capability == "object-storage":
        return Connection(
            # A bucket is addressed by name, not by host: a variable holding
            # just "blobs" wants the bucket id, while a URL wants the domain.
            host=f"${{aws_s3_bucket.{service.name}_bucket.bucket_domain_name}}",
            name=f"${{aws_s3_bucket.{service.name}_bucket.id}}",
            addressed_by="name",
            # S3 is reached over the scheme's default port.
            port=None,
        )

    return None


# Listener rule priorities must be unique across every application sharing a
# listener, so they cannot simply start at 1. Each application gets a band
# derived from its name, and its routes are ordered within that band by path
# specificity, longest first, so that /api/admin is matched before /api.
_PRIORITY_BANDS = 500
_BAND_WIDTH = 100


def _priority_band(app_name: str) -> int:
    digest = hashlib.sha256(app_name.encode()).digest()
    return 1 + (int.from_bytes(digest[:4], "big") % _PRIORITY_BANDS) * _BAND_WIDTH


def _listener_priorities(app: SemanticApp) -> dict[str, int]:
    """Assign each public service a stable, unique listener rule priority."""
    band = _priority_band(app.name)
    ordered = sorted(app.public_services, key=lambda s: (-len(s.ingress.path), s.name))

    priorities: dict[str, int] = {}
    for offset, service in enumerate(ordered):
        priorities[service.name] = (
            service.ingress.priority
            if service.ingress.priority is not None
            else band + offset
        )
    return priorities


def _path_patterns(path: str) -> list[str]:
    """ALB path patterns matching a prefix and everything beneath it."""
    if path == "/":
        return ["/*"]
    trimmed = path.rstrip("/")
    return [trimmed, f"{trimmed}/*"]


# Written into every secret composey creates but cannot value. Recognisable in
# a console, and obviously not a working credential if one reaches an app.
PLACEHOLDER = "PLACEHOLDER_VALUE_CHANGE_IN_AWS_CONSOLE"


def _safe(name: str) -> str:
    """A Terraform-safe identifier fragment."""
    return "".join(c if c.isalnum() else "_" for c in name).strip("_")


def infer(app: SemanticApp, env: AwsEnvironment) -> AWSResources:
    resources = AWSResources()

    # Naming convention helper: [env]-[app]-[resource]
    def get_name(resource_name: str) -> str:
        return f"{env.name}-{app.name}-{resource_name}"

    # Helper for tags
    tags = env.tags if env.tags else None

    # 1. One security group per compose network. Services sharing a network can
    # reach each other and services on disjoint networks cannot, which is what
    # Compose already enforces locally.
    networks = sorted({n for service in app.services for n in service.networks})

    def sg_key(network: str) -> str:
        return f"{_safe(network)}_sg"

    def sg_ids(service_networks: list[str]) -> list[str]:
        return [
            f"${{aws_security_group.{sg_key(n)}.id}}" for n in sorted(service_networks)
        ]

    for network in networks:
        key = sg_key(network)
        resources.aws_security_group[key] = SecurityGroup(
            name=get_name(network),
            vpc_id=env.vpc_id,
            description=f"Network {network} of {app.name} in {env.name}",
            tags=tags,
        )

        # Members of a network reach each other on any port, as they do under
        # Compose. Ports are not part of what a compose network states.
        resources.aws_security_group_rule[f"{key}_internal_rule"] = SecurityGroupRule(
            type="ingress",
            from_port=0,
            to_port=0,
            protocol="-1",
            security_group_id=f"${{aws_security_group.{key}.id}}",
            source_security_group_id=f"${{aws_security_group.{key}.id}}",
            description=f"Allow traffic within network {network}",
        )

        # Declaring an aws_security_group with no inline egress makes Terraform
        # strip AWS's default allow-all egress, leaving tasks with no outbound
        # access (they can't pull images or write logs).
        resources.aws_security_group_rule[f"{key}_egress_rule"] = SecurityGroupRule(
            type="egress",
            from_port=0,
            to_port=0,
            protocol="-1",
            security_group_id=f"${{aws_security_group.{key}.id}}",
            cidr_blocks=["0.0.0.0/0"],
            description=f"Allow all outbound from network {network}",
        )

    priorities = _listener_priorities(app)

    # 2. Map each Semantic Service to AWS resources
    for service in app.services:
        # Define compute sizes (Fargate units)
        size_map = {
            "small": {"cpu": 256, "memory": 512},
            "medium": {"cpu": 1024, "memory": 2048},
            "large": {"cpu": 4096, "memory": 8192},
        }
        compute = size_map.get(service.size, size_map["small"])

        # Override with explicit service-level CPU/Memory if provided
        if service.cpu is not None:
            compute["cpu"] = service.cpu
        if service.memory is not None:
            compute["memory"] = service.memory

        if service.capability == "database":
            # Managed RDS Instance
            engine = "postgres"
            if "mysql" in service.image.lower():
                engine = "mysql"
            elif "mariadb" in service.image.lower():
                engine = "mariadb"

            # "admin" is a reserved master username on RDS Postgres; use a
            # non-reserved name that is valid across engines.
            db_username = "composey"

            # 1. Create a random master password
            password_key = f"{service.name}_password"
            resources.random_password[password_key] = RandomPassword(length=20)

            # 2. Store credentials in Secrets Manager
            db_secret_key = f"{service.name}_db_secret"
            resources.aws_secretsmanager_secret[db_secret_key] = SecretsManagerSecret(
                name=get_name(f"{service.name}-credentials"),
                description=f"Credentials for {service.name} RDS",
                tags=tags,
            )

            # 3. Create the secret version (Initial credentials)
            resources.aws_secretsmanager_secret_version[f"{db_secret_key}_v1"] = (
                SecretsManagerSecretVersion(
                    secret_id=f"${{aws_secretsmanager_secret.{db_secret_key}.id}}",
                    secret_string=json.dumps(
                        {
                            "username": db_username,
                            "password": f"${{random_password.{password_key}.result}}",
                            "engine": engine,
                        }
                    ),
                )
            )

            sng_key = f"{service.name}_sng"
            resources.aws_db_subnet_group[sng_key] = DbSubnetGroup(
                name=get_name(f"{service.name}-sng"),
                subnet_ids=env.private_subnets,
                tags=tags,
            )

            # Map x-composey size to RDS instance classes
            db_instance_classes = {
                "small": "db.t3.micro",
                "medium": "db.t3.medium",
                "large": "db.m5.large",
            }

            db_key = f"{service.name}_db"
            resources.aws_db_instance[db_key] = DbInstance(
                identifier=get_name(service.name),
                engine=engine,
                instance_class=db_instance_classes.get(
                    service.size, db_instance_classes["small"]
                ),
                allocated_storage=20,
                db_subnet_group_name=f"${{aws_db_subnet_group.{sng_key}.name}}",
                vpc_security_group_ids=sg_ids(service.networks),
                skip_final_snapshot=True,
                publicly_accessible=False,
                username=db_username,
                password=f"${{random_password.{password_key}.result}}",
                tags=tags,
            )
            continue

        if service.capability == "cache":
            # Managed ElastiCache (Redis)
            sng_key = f"{service.name}_sng"
            resources.aws_elasticache_subnet_group[sng_key] = ElastiCacheSubnetGroup(
                name=get_name(f"{service.name}-sng"),
                subnet_ids=env.private_subnets,
                tags=tags,
            )

            # Map size to ElastiCache node types
            cache_node_types = {
                "small": "cache.t3.micro",
                "medium": "cache.t3.medium",
                "large": "cache.m5.large",
            }

            cache_key = f"{service.name}_cache"
            resources.aws_elasticache_cluster[cache_key] = ElastiCacheCluster(
                cluster_id=get_name(service.name),
                engine="redis",
                node_type=cache_node_types.get(service.size, cache_node_types["small"]),
                num_cache_nodes=1,
                subnet_group_name=f"${{aws_elasticache_subnet_group.{sng_key}.name}}",
                security_group_ids=sg_ids(service.networks),
                tags=tags,
            )
            continue

        if service.capability == "object-storage":
            # Managed S3 Bucket (Minio substitution)
            bucket_key = f"{service.name}_bucket"
            resources.aws_s3_bucket[bucket_key] = S3Bucket(
                bucket=get_name(service.name)
                .lower()
                .replace("_", "-")[:63]
                .rstrip("-"),
                force_destroy=True,
                tags=tags,
            )
            continue

        # Standard Container (ECS Fargate)
        # 1. Create a Log Group
        log_group_key = f"{service.name}_lg"
        resources.aws_cloudwatch_log_group[log_group_key] = CloudWatchLogGroup(
            name=f"/ecs/{get_name(service.name)}",
            retention_in_days=env.log_retention_days,
            tags=tags,
        )

        # 2. Create IAM Roles for the service
        task_role_key = f"{service.name}_task_role"
        resources.aws_iam_role[task_role_key] = IamRole(
            name=get_name(f"{service.name}-task-role"),
            assume_role_policy=json.dumps(
                {
                    "Version": "2012-10-17",
                    "Statement": [
                        {
                            "Action": "sts:AssumeRole",
                            "Effect": "Allow",
                            "Principal": {"Service": "ecs-tasks.amazonaws.com"},
                        }
                    ],
                }
            ),
            tags=tags,
        )

        exec_role_key = f"{service.name}_exec_role"
        resources.aws_iam_role[exec_role_key] = IamRole(
            name=get_name(f"{service.name}-exec-role"),
            assume_role_policy=json.dumps(
                {
                    "Version": "2012-10-17",
                    "Statement": [
                        {
                            "Action": "sts:AssumeRole",
                            "Effect": "Allow",
                            "Principal": {"Service": "ecs-tasks.amazonaws.com"},
                        }
                    ],
                }
            ),
            tags=tags,
        )

        # 3. Grant Exec Role permission to push logs
        exec_log_policy_key = f"{service.name}_exec_log_policy"
        resources.aws_iam_role_policy[exec_log_policy_key] = IamRolePolicy(
            name=get_name(f"{service.name}-exec-log-policy"),
            role=f"${{aws_iam_role.{exec_role_key}.name}}",
            policy=json.dumps(
                {
                    "Version": "2012-10-17",
                    "Statement": [
                        {
                            "Effect": "Allow",
                            "Action": [
                                "logs:CreateLogStream",
                                "logs:PutLogEvents",
                            ],
                            "Resource": [
                                f"${{aws_cloudwatch_log_group.{log_group_key}.arn}}:*"
                            ],
                        }
                    ],
                }
            ),
        )

        # Build-from-source: provision ECR and build+push the image via Terraform
        # (kreuzwerker/docker) so a `build:` service deploys without a prebuilt image.
        container_image = service.image
        if service.build_context:
            ecr_key = f"{service.name}_ecr"
            resources.aws_ecr_repository[ecr_key] = EcrRepository(
                name=get_name(service.name).lower(),
                tags=tags,
            )

            image_key = f"{service.name}_image"
            build = {
                "context": service.build_context,
                # Pin to amd64 to match Fargate's default X86_64 platform, so images
                # built on an arm64 host (e.g. Apple Silicon) still run on ECS.
                "platform": "linux/amd64",
            }
            if service.dockerfile:
                # Monorepos build several services from one context, each with its
                # own Dockerfile. Dropping this built the wrong image, or failed
                # outright when no Dockerfile sat at the context root.
                build["dockerfile"] = service.dockerfile
            resources.docker_image[image_key] = DockerImage(
                name=f"${{aws_ecr_repository.{ecr_key}.repository_url}}:latest",
                build=build,
            )

            push_key = f"{service.name}_push"
            resources.docker_registry_image[push_key] = DockerRegistryImage(
                name=f"${{docker_image.{image_key}.name}}",
                keep_remotely=True,
            )

            # Reference the pushed digest so ECS redeploys when the image changes.
            container_image = (
                f"${{aws_ecr_repository.{ecr_key}.repository_url}}"
                f"@${{docker_registry_image.{push_key}.sha256_digest}}"
            )

            # The execution role must be able to pull from the ECR repo.
            ecr_pull_policy_key = f"{service.name}_exec_ecr_policy"
            resources.aws_iam_role_policy[ecr_pull_policy_key] = IamRolePolicy(
                name=get_name(f"{service.name}-exec-ecr-policy"),
                role=f"${{aws_iam_role.{exec_role_key}.name}}",
                policy=json.dumps(
                    {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Effect": "Allow",
                                "Action": [
                                    "ecr:GetAuthorizationToken",
                                    "ecr:BatchCheckLayerAvailability",
                                    "ecr:GetDownloadUrlForLayer",
                                    "ecr:BatchGetImage",
                                ],
                                "Resource": "*",
                            }
                        ],
                    }
                ),
            )

        # Resolve storage to S3 buckets and IAM policies. A named volume is one
        # bucket keyed on the volume, not on the service: compose files share a
        # volume between services deliberately, and a bucket each would silently
        # stop them sharing anything.
        for bucket_name in service.storage:
            safe_id = "".join(c if c.isalnum() else "_" for c in bucket_name).strip("_")
            bucket_key = f"{safe_id}_volume_bucket"

            resources.aws_s3_bucket[bucket_key] = S3Bucket(
                bucket=get_name(safe_id).lower().replace("_", "-")[:63].rstrip("-"),
                force_destroy=True,
                tags=tags,
            )

            policy_key = f"{service.name}_{safe_id}_policy"
            resources.aws_iam_role_policy[policy_key] = IamRolePolicy(
                name=get_name(f"{service.name}-{safe_id}-policy"),
                role=f"${{aws_iam_role.{task_role_key}.name}}",
                policy=json.dumps(
                    {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Effect": "Allow",
                                "Action": ["s3:*"],
                                "Resource": [
                                    f"${{aws_s3_bucket.{bucket_key}.arn}}",
                                    f"${{aws_s3_bucket.{bucket_key}.arn}}/*",
                                ],
                            }
                        ],
                    }
                ),
            )

        # Resolve secrets to AWS Secrets Manager references
        container_secrets = []
        for secret_name in service.secrets:
            secret_key = f"{service.name}_{secret_name}_secret"
            resources.aws_secretsmanager_secret[secret_key] = SecretsManagerSecret(
                name=get_name(f"{service.name}-{secret_name}"),
                description=f"Secret {secret_name} for {app.name} service {service.name}",
                tags=tags,
            )

            # Create a placeholder secret version so the secret is not empty
            # Use ignore_changes so operators can update the value in AWS Console
            resources.aws_secretsmanager_secret_version[f"{secret_key}_v1"] = (
                SecretsManagerSecretVersion(
                    secret_id=f"${{aws_secretsmanager_secret.{secret_key}.id}}",
                    secret_string=PLACEHOLDER,
                    lifecycle=TerraformLifecycle(ignore_changes=["secret_string"]),
                )
            )

            container_secrets.append(
                {
                    "name": secret_name.upper().replace("-", "_"),
                    "valueFrom": f"${{aws_secretsmanager_secret.{secret_key}.arn}}",
                }
            )

            # Grant Exec Role access to read the secret
            secret_policy_key = f"{service.name}_{secret_name}_policy"
            resources.aws_iam_role_policy[secret_policy_key] = IamRolePolicy(
                name=get_name(f"{service.name}-{secret_name}-policy"),
                role=f"${{aws_iam_role.{exec_role_key}.name}}",
                policy=json.dumps(
                    {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Effect": "Allow",
                                "Action": ["secretsmanager:GetSecretValue"],
                                "Resource": [
                                    f"${{aws_secretsmanager_secret.{secret_key}.arn}}"
                                ],
                            }
                        ],
                    }
                ),
            )

        # Variables the compose file names but does not value. One secret per
        # service holding them all, rather than one secret each: ECS can pull an
        # individual key out of a JSON secret, and Secrets Manager bills per
        # secret.
        if service.config:
            config_key = f"{service.name}_config"
            resources.aws_secretsmanager_secret[config_key] = SecretsManagerSecret(
                name=get_name(f"{service.name}-config"),
                description=f"Platform-supplied configuration for {service.name}",
                tags=tags,
            )
            resources.aws_secretsmanager_secret_version[f"{config_key}_v1"] = (
                SecretsManagerSecretVersion(
                    secret_id=f"${{aws_secretsmanager_secret.{config_key}.id}}",
                    secret_string=json.dumps(
                        {key: PLACEHOLDER for key in service.config}
                    ),
                    # Real values are set outside Terraform, so a later apply
                    # must not put the placeholders back.
                    lifecycle=TerraformLifecycle(ignore_changes=["secret_string"]),
                )
            )

            container_secrets.extend(
                {
                    "name": key,
                    "valueFrom": (
                        f"${{aws_secretsmanager_secret.{config_key}.arn}}:{key}::"
                    ),
                }
                for key in service.config
            )

            resources.aws_iam_role_policy[f"{service.name}_config_policy"] = (
                IamRolePolicy(
                    name=get_name(f"{service.name}-config-policy"),
                    role=f"${{aws_iam_role.{exec_role_key}.name}}",
                    policy=json.dumps(
                        {
                            "Version": "2012-10-17",
                            "Statement": [
                                {
                                    "Effect": "Allow",
                                    "Action": ["secretsmanager:GetSecretValue"],
                                    "Resource": [
                                        f"${{aws_secretsmanager_secret.{config_key}.arn}}"
                                    ],
                                }
                            ],
                        }
                    ),
                )
            )

        # Container Definition
        container = ContainerDefinition(
            name=service.name,
            image=container_image,
            command=service.command,
            portMappings=[
                {
                    "containerPort": service.port,
                    "hostPort": service.port,
                    "protocol": "tcp",
                }
            ]
            if service.port
            else [],
            environment=[{"name": k, "value": v} for k, v in service.env.items()],
            secrets=container_secrets,
            logConfiguration={
                "logDriver": "awslogs",
                "options": {
                    "awslogs-group": f"${{aws_cloudwatch_log_group.{log_group_key}.name}}",
                    "awslogs-region": env.region,
                    "awslogs-stream-prefix": "ecs",
                },
            },
        )

        # Task Definition
        task_def_key = f"{service.name}_td"
        resources.aws_ecs_task_definition[task_def_key] = EcsTaskDefinition(
            family=get_name(service.name),
            cpu=str(compute["cpu"]),
            memory=str(compute["memory"]),
            container_definitions=json.dumps([container.model_dump(exclude_none=True)]),
            execution_role_arn=f"${{aws_iam_role.{exec_role_key}.arn}}",
            task_role_arn=f"${{aws_iam_role.{task_role_key}.arn}}",
            tags=tags,
        )

        # ECS Service
        service_key = f"{service.name}_service"
        ecs_service = EcsService(
            name=get_name(service.name),
            cluster=env.ecs_cluster_arn,
            task_definition=f"${{aws_ecs_task_definition.{task_def_key}.arn}}",
            health_check_grace_period_seconds=service.startup_grace_period,
            network_configuration={
                "subnets": env.private_subnets,
                "security_groups": sg_ids(service.networks),
                "assign_public_ip": False,
            },
            tags=tags,
        )

        # 4. Handle Public Ingress (ALB integration)
        if service.ingress and env.alb_arn and not service.schedule:
            ingress = service.ingress
            ingress_port = ingress.port or service.port or 80

            tg_key = f"{service.name}_tg"
            resources.aws_lb_target_group[tg_key] = LbTargetGroup(
                name=get_name(f"{service.name}-tg"),
                port=ingress_port,
                protocol="HTTP",
                vpc_id=env.vpc_id,
                target_type="ip",
                health_check={
                    "enabled": True,
                    "path": ingress.health_path,
                    "matcher": "200-399",
                },
                tags=tags,
            )

            if env.alb_listener_arn:
                rule_key = f"{service.name}_listener_rule"
                resources.aws_lb_listener_rule[rule_key] = LbListenerRule(
                    listener_arn=env.alb_listener_arn,
                    priority=priorities[service.name],
                    action=[
                        {
                            "type": "forward",
                            "target_group_arn": f"${{aws_lb_target_group.{tg_key}.arn}}",
                        }
                    ],
                    condition=[
                        {"path_pattern": {"values": _path_patterns(ingress.path)}}
                    ],
                )

            ecs_service.load_balancer = [
                {
                    "target_group_arn": f"${{aws_lb_target_group.{tg_key}.arn}}",
                    "container_name": service.name,
                    "container_port": ingress_port,
                }
            ]

            # A dedicated group, attached to this service alone. Putting the
            # rule on a network group instead would open the port for every
            # other service on that network.
            ingress_sg_key = f"{_safe(service.name)}_ingress_sg"
            resources.aws_security_group[ingress_sg_key] = SecurityGroup(
                name=get_name(f"{service.name}-ingress"),
                vpc_id=env.vpc_id,
                description=f"Load balancer ingress to {service.name}",
                tags=tags,
            )
            resources.aws_security_group_rule[f"alb_to_{service.name}_rule"] = (
                SecurityGroupRule(
                    type="ingress",
                    from_port=ingress_port,
                    to_port=ingress_port,
                    protocol="tcp",
                    security_group_id=f"${{aws_security_group.{ingress_sg_key}.id}}",
                    source_security_group_id=env.alb_security_group_id,
                    description=f"Allow the load balancer to reach {service.name}",
                )
            )
            ecs_service.network_configuration["security_groups"] = [
                *ecs_service.network_configuration["security_groups"],
                f"${{aws_security_group.{ingress_sg_key}.id}}",
            ]

            # 4b. Handle CloudFront CDN
            if service.cdn_enabled:
                waf_key = f"{service.name}_waf"
                resources.aws_wafv2_web_acl[waf_key] = Wafv2WebAcl(
                    name=get_name(f"{service.name}-waf"),
                    scope="CLOUDFRONT",
                    # CLOUDFRONT-scoped ACLs only exist in us-east-1.
                    provider=CLOUDFRONT_PROVIDER_REF,
                    visibility_config={
                        "cloudwatch_metrics_enabled": True,
                        "metric_name": f"{service.name}WAF",
                        "sampled_requests_enabled": True,
                    },
                    rule=[
                        {
                            "name": "AWS-AWSManagedRulesCommonRuleSet",
                            "priority": 1,
                            "override_action": {"none": {}},
                            "statement": {
                                "managed_rule_group_statement": {
                                    "name": "AWSManagedRulesCommonRuleSet",
                                    "vendor_name": "AWS",
                                }
                            },
                            "visibility_config": {
                                "cloudwatch_metrics_enabled": True,
                                "metric_name": "AWSManagedRulesCommonRuleSet",
                                "sampled_requests_enabled": True,
                            },
                        }
                    ],
                )

                cdn_key = f"{service.name}_cdn"
                # A CloudFront origin needs the ALB's DNS name, which cannot be
                # derived from its ARN. The generator emits a matching
                # data.aws_lb block so the name is resolved at apply time.
                resources.aws_cloudfront_distribution[cdn_key] = CloudfrontDistribution(
                    comment=f"CDN for {service.name}",
                    origin=[
                        {
                            "domain_name": f"${{data.aws_lb.{ALB_DATA_SOURCE_KEY}.dns_name}}",
                            "origin_id": "ALB",
                            "custom_origin_config": {
                                "http_port": 80,
                                "https_port": 443,
                                "origin_protocol_policy": "http-only",
                                "origin_ssl_protocols": ["TLSv1.2"],
                            },
                        }
                    ],
                    default_cache_behavior={
                        "allowed_methods": [
                            "DELETE",
                            "GET",
                            "HEAD",
                            "OPTIONS",
                            "PATCH",
                            "POST",
                            "PUT",
                        ],
                        "cached_methods": ["GET", "HEAD"],
                        "target_origin_id": "ALB",
                        "viewer_protocol_policy": "redirect-to-https",
                        "forwarded_values": {
                            "query_string": True,
                            "cookies": {"forward": "all"},
                        },
                    },
                    web_acl_id=f"${{aws_wafv2_web_acl.{waf_key}.arn}}",
                    tags=tags,
                )

        # Only create an ECS Service if it's NOT a scheduled task
        if not service.schedule:
            resources.aws_ecs_service[service_key] = ecs_service
        else:
            # Scheduled Task (EventBridge)
            rule_key = f"{service.name}_rule"
            resources.aws_cloudwatch_event_rule[rule_key] = CloudwatchEventRule(
                name=get_name(f"{service.name}-rule"),
                schedule_expression=_eventbridge_expression(service.schedule),
                description=f"Schedule for {service.name}",
                tags=tags,
            )

            # We need an IAM role for EventBridge to run tasks
            eb_role_key = f"{service.name}_eb_role"
            resources.aws_iam_role[eb_role_key] = IamRole(
                name=get_name(f"{service.name}-eb-role"),
                assume_role_policy=json.dumps(
                    {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Action": "sts:AssumeRole",
                                "Effect": "Allow",
                                "Principal": {"Service": "events.amazonaws.com"},
                            }
                        ],
                    }
                ),
                tags=tags,
            )

            eb_policy_key = f"{service.name}_eb_policy"
            resources.aws_iam_role_policy[eb_policy_key] = IamRolePolicy(
                name=get_name(f"{service.name}-eb-policy"),
                role=f"${{aws_iam_role.{eb_role_key}.name}}",
                policy=json.dumps(
                    {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Effect": "Allow",
                                "Action": "ecs:RunTask",
                                "Resource": [
                                    f"${{aws_ecs_task_definition.{task_def_key}.arn}}"
                                ],
                                "Condition": {
                                    "ArnLike": {"ecs:cluster": f"{env.ecs_cluster_arn}"}
                                },
                            },
                            {
                                "Effect": "Allow",
                                "Action": "iam:PassRole",
                                "Resource": ["*"],
                                "Condition": {
                                    "StringLike": {
                                        "iam:PassedToService": "ecs-tasks.amazonaws.com"
                                    }
                                },
                            },
                        ],
                    }
                ),
            )

            resources.aws_cloudwatch_event_target[f"{service.name}_target"] = (
                CloudwatchEventTarget(
                    rule=f"${{aws_cloudwatch_event_rule.{rule_key}.name}}",
                    arn=env.ecs_cluster_arn,
                    role_arn=f"${{aws_iam_role.{eb_role_key}.arn}}",
                    ecs_target={
                        "task_count": 1,
                        "task_definition_arn": f"${{aws_ecs_task_definition.{task_def_key}.arn}}",
                        "launch_type": "FARGATE",
                        "network_configuration": {
                            "subnets": env.private_subnets,
                            "security_groups": sg_ids(service.networks),
                            "assign_public_ip": False,
                        },
                    },
                )
            )

        # 5. Handle Auto-scaling
        if service.max_scale > 1:
            target_key = f"{service.name}_asg_target"
            resources.aws_appautoscaling_target[target_key] = AppAutoscalingTarget(
                max_capacity=service.max_scale,
                min_capacity=service.min_scale,
                resource_id=f'service/${{split("/", "${{aws_ecs_service.{service_key}.cluster}}")[1]}}/${{aws_ecs_service.{service_key}.name}}',
            )

            # CPU Scaling Policy
            cpu_policy_key = f"{service.name}_cpu_scaling"
            resources.aws_appautoscaling_policy[cpu_policy_key] = AppAutoscalingPolicy(
                name=get_name(f"{service.name}-cpu-scaling"),
                resource_id=f"${{aws_appautoscaling_target.{target_key}.resource_id}}",
                target_tracking_scaling_policy_configuration={
                    "predefined_metric_specification": {
                        "predefined_metric_type": "ECSServiceAverageCPUUtilization"
                    },
                    "target_value": 70.0,
                },
            )

            # Memory Scaling Policy
            mem_policy_key = f"{service.name}_mem_scaling"
            resources.aws_appautoscaling_policy[mem_policy_key] = AppAutoscalingPolicy(
                name=get_name(f"{service.name}-mem-scaling"),
                resource_id=f"${{aws_appautoscaling_target.{target_key}.resource_id}}",
                target_tracking_scaling_policy_configuration={
                    "predefined_metric_specification": {
                        "predefined_metric_type": "ECSServiceAverageMemoryUtilization"
                    },
                    "target_value": 80.0,
                },
            )

    # How each substituted service is reached, built once and shared by
    # environment wiring and security group rules.
    connections = {
        service.name: connection
        for service in app.services
        if (connection := _connection_for(service)) is not None
    }

    # 3. Dynamic Link Injection (Service Discovery)
    # If a service depends on a managed capability, inject the connection details
    for service in app.services:
        if service.capability != "container":
            continue

        service_key = f"{service.name}_service"
        # We need to find the ECS task definition for this service
        task_def_key = f"{service.name}_td"
        if task_def_key not in resources.aws_ecs_task_definition:
            continue

        task_def = resources.aws_ecs_task_definition[task_def_key]
        container_defs = json.loads(task_def.container_definitions)
        container = container_defs[0]

        exec_role_key = f"{service.name}_exec_role"

        # Check all relationships where this service is the client
        for rel in [r for r in app.relationships if r.client == service.name]:
            server = next((s for s in app.services if s.name == rel.server), None)
            if not server or server.capability == "container":
                continue

            # Server is a managed service (DB, Cache, or S3)
            if server.capability == "database":
                db_secret_key = f"{server.name}_db_secret"

                # Inject credentials from the RDS secret
                container["secrets"].extend(
                    [
                        {
                            "name": "DB_PASSWORD",
                            "valueFrom": f"${{aws_secretsmanager_secret.{db_secret_key}.arn}}:password::",
                        },
                        {
                            "name": "DB_USERNAME",
                            "valueFrom": f"${{aws_secretsmanager_secret.{db_secret_key}.arn}}:username::",
                        },
                    ]
                )

                # Grant Exec Role access to the RDS secret
                rds_secret_policy_key = f"{service.name}_to_{server.name}_rds_secret"
                resources.aws_iam_role_policy[rds_secret_policy_key] = IamRolePolicy(
                    name=get_name(f"{service.name}-{server.name}-rds-secret"),
                    role=f"${{aws_iam_role.{exec_role_key}.name}}",
                    policy=json.dumps(
                        {
                            "Version": "2012-10-17",
                            "Statement": [
                                {
                                    "Effect": "Allow",
                                    "Action": ["secretsmanager:GetSecretValue"],
                                    "Resource": [
                                        f"${{aws_secretsmanager_secret.{db_secret_key}.arn}}"
                                    ],
                                }
                            ],
                        }
                    ),
                )

            elif server.capability == "object-storage":
                bucket_key = f"{server.name}_bucket"

                # Also grant IAM permissions to the client service
                policy_key = f"{service.name}_to_{server.name}_s3_policy"
                resources.aws_iam_role_policy[policy_key] = IamRolePolicy(
                    name=get_name(f"{service.name}-{server.name}-s3-policy"),
                    role=f"${{aws_iam_role.{service.name}_task_role.name}}",
                    policy=json.dumps(
                        {
                            "Version": "2012-10-17",
                            "Statement": [
                                {
                                    "Effect": "Allow",
                                    "Action": ["s3:*"],
                                    "Resource": [
                                        f"${{aws_s3_bucket.{bucket_key}.arn}}",
                                        f"${{aws_s3_bucket.{bucket_key}.arn}}/*",
                                    ],
                                }
                            ],
                        }
                    ),
                )

        # Point every reference to a managed service at the real thing. Driven
        # by the values the compose file already carries, not by variable names.
        container["environment"] = resolve_environment(
            container["environment"], connections
        )

        task_def.container_definitions = json.dumps(container_defs)

    # Connectivity is not derived from relationships. depends_on describes
    # startup order and constrains nothing under Compose, so rules built from it
    # were guesses; network membership above is the compose file's own answer.
    # Relationships still say who gets whose endpoint and credentials.

    return resources
