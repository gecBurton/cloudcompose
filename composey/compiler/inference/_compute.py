"""Compute resources inference (ECS Fargate).

Handles inference of container-based compute resources including ECS services,
task definitions, IAM roles, and auto-scaling.
"""

import json
from typing import Callable

from composey.constants import (
    AWS_LOGS_STREAM_PREFIX,
    SIZE_MAPPINGS,
)
from composey.models.aws import (
    AppAutoscalingPolicy,
    AppAutoscalingTarget,
    AWSResources,
    CloudWatchLogGroup,
    ContainerDefinition,
    DockerImage,
    DockerRegistryImage,
    EcrRepository,
    EcsService,
    EcsTaskDefinition,
    IamRole,
    IamRolePolicy,
    LbListenerRule,
    LbTargetGroup,
    SecretsManagerSecret,
    SecretsManagerSecretVersion,
    TerraformLifecycle,
)
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application as SemanticApp
from composey.models.semantic import Connection
from composey.models.semantic import Service as SemanticService

from ._common import path_patterns, safe_terraform_identifier
from ._connectivity import _is_discoverable, security_group_ids


def infer_compute_resources(
    resources: AWSResources,
    app: SemanticApp,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    discard: bool,
    priorities: dict[str, int],
    namespace: str,
) -> dict[str, Connection]:
    """Infer ECS Fargate compute resources.

    Returns a mapping of discoverable service names to their connections.
    """
    connections: dict[str, Connection] = {}

    for service in app.services:
        if service.capability != "container":
            continue

        # Get compute sizing
        compute = SIZE_MAPPINGS.get(service.size, SIZE_MAPPINGS["small"]).copy()
        if service.cpu is not None:
            compute["cpu"] = service.cpu
        if service.memory is not None:
            compute["memory"] = service.memory

        # Create log group
        log_group_key = f"{service.name}_lg"
        resources.aws_cloudwatch_log_group[log_group_key] = CloudWatchLogGroup(
            name=f"/ecs/{get_name(service.name)}",
            retention_in_days=env.log_retention_days,
            tags=tags,
        )

        # Create IAM roles
        task_role_key, exec_role_key = _create_iam_roles(
            resources, service, get_name, tags
        )

        # Grant exec role permission to write logs
        _create_log_policy(resources, service, log_group_key, get_name, exec_role_key)

        # Handle build-from-source
        container_image = _handle_build_context(
            resources, service, env, get_name, tags, discard, exec_role_key
        )

        # Handle secrets
        container_secrets = _handle_secrets(
            resources, service, app, get_name, tags, exec_role_key
        )

        # Handle platform config (env vars valued outside compose file)
        _handle_platform_config(
            resources, service, app, get_name, tags, container_secrets
        )

        # Create container definition
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
                    "awslogs-stream-prefix": AWS_LOGS_STREAM_PREFIX,
                },
            },
        )

        # Create task definition
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

        # Create ECS service
        ecs_service = _create_ecs_service(
            resources,
            service,
            env,
            get_name,
            tags,
            task_def_key,
        )

        # Handle public ingress
        if service.ingress and env.alb_arn and not service.schedule:
            _handle_ingress(
                resources,
                service,
                env,
                get_name,
                tags,
                priorities,
                ecs_service,
            )

        # Only create service if not scheduled
        if not service.schedule:
            resources.aws_ecs_service[f"{service.name}_service"] = ecs_service

            # Handle auto-scaling
            if service.max_scale > 1:
                _handle_autoscaling(resources, service, env, get_name, tags)

        # Add connection if discoverable
        if _is_discoverable(service):
            connections[service.name] = Connection(
                host=f"{service.name}.{namespace}",
                port=service.port,
            )

    return connections


def _create_iam_roles(
    resources: AWSResources,
    service: SemanticService,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> tuple[str, str]:
    """Create IAM roles for ECS task and execution."""
    from composey.constants import IAM_POLICY_VERSION

    task_role_key = f"{service.name}_task_role"
    resources.aws_iam_role[task_role_key] = IamRole(
        name=get_name(f"{service.name}-task-role"),
        assume_role_policy=json.dumps(
            {
                "Version": IAM_POLICY_VERSION,
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
                "Version": IAM_POLICY_VERSION,
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

    return task_role_key, exec_role_key


def _create_log_policy(
    resources: AWSResources,
    service: SemanticService,
    log_group_key: str,
    get_name: Callable[[str], str],
    exec_role_key: str,
) -> None:
    """Grant exec role permission to push logs to CloudWatch."""
    from composey.constants import IAM_POLICY_VERSION

    exec_log_policy_key = f"{service.name}_exec_log_policy"
    resources.aws_iam_role_policy[exec_log_policy_key] = IamRolePolicy(
        name=get_name(f"{service.name}-exec-log-policy"),
        role=f"${{aws_iam_role.{exec_role_key}.name}}",
        policy=json.dumps(
            {
                "Version": IAM_POLICY_VERSION,
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


def _handle_build_context(
    resources: AWSResources,
    service: SemanticService,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    discard: bool,
    exec_role_key: str,
) -> str:
    """Handle build-from-source services."""
    from composey.constants import IAM_POLICY_VERSION

    container_image = service.image

    if service.build_context:
        # Create ECR repository
        ecr_key = f"{service.name}_ecr"
        resources.aws_ecr_repository[ecr_key] = EcrRepository(
            name=get_name(service.name).lower(),
            force_delete=discard,
            tags=tags,
        )

        # Configure build
        build = {
            "context": service.build_context,
            "platform": "linux/amd64",  # Match Fargate's X86_64
        }
        if service.dockerfile:
            build["dockerfile"] = service.dockerfile

        image_key = f"{service.name}_image"
        resources.docker_image[image_key] = DockerImage(
            name=f"${{aws_ecr_repository.{ecr_key}.repository_url}}:latest",
            build=build,
        )

        push_key = f"{service.name}_push"
        resources.docker_registry_image[push_key] = DockerRegistryImage(
            name=f"${{docker_image.{image_key}.name}}",
            keep_remotely=True,
        )

        container_image = (
            f"${{aws_ecr_repository.{ecr_key}.repository_url}}"
            f"@${{docker_registry_image.{push_key}.sha256_digest}}"
        )

        # Grant ECR pull permissions
        ecr_pull_policy_key = f"{service.name}_exec_ecr_policy"
        resources.aws_iam_role_policy[ecr_pull_policy_key] = IamRolePolicy(
            name=get_name(f"{service.name}-exec-ecr-policy"),
            role=f"${{aws_iam_role.{exec_role_key}.name}}",
            policy=json.dumps(
                {
                    "Version": IAM_POLICY_VERSION,
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

    return container_image


def _handle_secrets(
    resources: AWSResources,
    service: SemanticService,
    app: SemanticApp,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    exec_role_key: str,
) -> list[dict]:
    """Handle compose secrets and create Secrets Manager resources."""
    from composey.constants import IAM_POLICY_VERSION, SECRETS_PLACEHOLDER_VALUE

    container_secrets = []

    for secret_name in service.secrets:
        secret_key = f"{service.name}_{secret_name}_secret"
        resources.aws_secretsmanager_secret[secret_key] = SecretsManagerSecret(
            name=get_name(f"{service.name}-{secret_name}"),
            description=f"Secret {secret_name} for {app.name} service {service.name}",
            tags=tags,
        )

        resources.aws_secretsmanager_secret_version[f"{secret_key}_v1"] = (
            SecretsManagerSecretVersion(
                secret_id=f"${{aws_secretsmanager_secret.{secret_key}.id}}",
                secret_string=SECRETS_PLACEHOLDER_VALUE,
                lifecycle=TerraformLifecycle(ignore_changes=["secret_string"]),
            )
        )

        container_secrets.append(
            {
                "name": secret_name.upper().replace("-", "_"),
                "valueFrom": f"${{aws_secretsmanager_secret.{secret_key}.arn}}",
            }
        )

        # Grant read access
        secret_policy_key = f"{service.name}_{secret_name}_policy"
        resources.aws_iam_role_policy[secret_policy_key] = IamRolePolicy(
            name=get_name(f"{service.name}-{secret_name}-policy"),
            role=f"${{aws_iam_role.{exec_role_key}.name}}",
            policy=json.dumps(
                {
                    "Version": IAM_POLICY_VERSION,
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

    return container_secrets


def _handle_platform_config(
    resources: AWSResources,
    service: SemanticService,
    app: SemanticApp,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    container_secrets: list[dict],
) -> None:
    """Handle platform-supplied configuration (env vars not valued in compose file)."""
    from composey.constants import IAM_POLICY_VERSION, SECRETS_PLACEHOLDER_VALUE

    if not service.config:
        return

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
                {key: SECRETS_PLACEHOLDER_VALUE for key in service.config}
            ),
            lifecycle=TerraformLifecycle(ignore_changes=["secret_string"]),
        )
    )

    container_secrets.extend(
        {
            "name": key,
            "valueFrom": f"${{aws_secretsmanager_secret.{config_key}.arn}}:{key}::",
        }
        for key in service.config
    )

    # Grant config read access
    resources.aws_iam_role_policy[f"{service.name}_config_policy"] = IamRolePolicy(
        name=get_name(f"{service.name}-config-policy"),
        role=f"${{aws_iam_role.{service.name}_exec_role.name}}",
        policy=json.dumps(
            {
                "Version": IAM_POLICY_VERSION,
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


def _create_ecs_service(
    resources: AWSResources,
    service: SemanticService,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    task_def_key: str,
) -> EcsService:
    """Create ECS service configuration."""
    from ._connectivity import _is_discoverable

    return EcsService(
        name=get_name(service.name),
        cluster=env.ecs_cluster_arn,
        task_definition=f"${{aws_ecs_task_definition.{task_def_key}.arn}}",
        desired_count=service.min_scale,
        lifecycle=(
            TerraformLifecycle(ignore_changes=["desired_count"])
            if service.max_scale > 1
            else None
        ),
        health_check_grace_period_seconds=service.startup_grace_period,
        network_configuration={
            "subnets": env.private_subnets,
            "security_groups": security_group_ids(service.network_isolation_segments),
            "assign_public_ip": False,
        },
        service_registries=(
            {
                "registry_arn": (
                    f"${{aws_service_discovery_service."
                    f"{safe_terraform_identifier(service.name)}_discovery.arn}}"
                )
            }
            if _is_discoverable(service)
            else None
        ),
        tags=tags,
    )


def _handle_ingress(
    resources: AWSResources,
    service: SemanticService,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    priorities: dict[str, int],
    ecs_service: EcsService,
) -> None:
    """Handle public ingress configuration (ALB)."""
    ingress = service.ingress
    ingress_port = ingress.port or service.port or 80

    # Create target group
    tg_key = f"{service.name}_tg"
    resources.aws_lb_target_group[tg_key] = LbTargetGroup(
        name=get_name(f"{service.name}-tg"),
        port=ingress_port,
        protocol="HTTP",
        vpc_id=env.vpc_id,
        target_type="ip",
        health_check={
            "enabled": True,
            "path": ingress.health_check.path if ingress.health_check else "/",
            "matcher": "200-399",
        },
        tags=tags,
    )

    # Create listener rule
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
            condition=[{"path_pattern": {"values": path_patterns(ingress.path)}}],
        )

    # Attach load balancer to service
    ecs_service.load_balancer = [
        {
            "target_group_arn": f"${{aws_lb_target_group.{tg_key}.arn}}",
            "container_name": service.name,
            "container_port": ingress_port,
        }
    ]

    # Create dedicated security group for ingress
    ingress_sg_key = f"{safe_terraform_identifier(service.name)}_ingress_sg"
    from composey.models.aws import SecurityGroup, SecurityGroupRule

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


def _handle_autoscaling(
    resources: AWSResources,
    service: SemanticService,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> None:
    """Handle auto-scaling configuration.

    Supports configurable metrics (CPU, memory, requests per target) with
    customizable target values and cooldown periods.
    """
    from composey.models.semantic import AutoScalingConfig

    service_key = f"{service.name}_service"
    target_key = f"{service.name}_asg_target"

    resources.aws_appautoscaling_target[target_key] = AppAutoscalingTarget(
        max_capacity=service.max_scale,
        min_capacity=service.min_scale,
        resource_id=f'service/${{split("/", "${{aws_ecs_service.{service_key}.cluster}}")[1]}}/${{aws_ecs_service.{service_key}.name}}',
    )

    # Get auto-scaling configuration (use defaults if not specified)
    config = service.auto_scaling or AutoScalingConfig()

    # Map semantic metrics to AWS metric types
    metric_mapping = {
        "cpu": "ECSServiceAverageCPUUtilization",
        "memory": "ECSServiceAverageMemoryUtilization",
        "requests_per_target": "ALBRequestCountPerTarget",
    }

    for i, metric in enumerate(config.metrics):
        policy_key = f"{service.name}_scaling_{i}"
        metric_name = metric_mapping.get(metric.type)

        if not metric_name:
            continue

        policy_config: dict = {
            "predefined_metric_specification": {"predefined_metric_type": metric_name},
            "target_value": metric.target_value,
            "scale_in_cooldown": config.scale_in_cooldown,
            "scale_out_cooldown": config.scale_out_cooldown,
        }

        # For ALB requests, we need to specify the resource label
        if metric.type == "requests_per_target":
            policy_config["predefined_metric_specification"]["resource_label"] = (
                f"${{aws_lb_target_group.{service.name}_tg.arn_suffix}}"
            )

        resources.aws_appautoscaling_policy[policy_key] = AppAutoscalingPolicy(
            name=get_name(f"{service.name}-scaling-{metric.type}"),
            resource_id=f"${{aws_appautoscaling_target.{target_key}.resource_id}}",
            target_tracking_scaling_policy_configuration=policy_config,
        )
