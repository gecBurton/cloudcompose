"""Tests for scripts/assert_managed.py.

The script only ever runs inside the real-AWS acceptance job, so without these
it would be as unproven as the deployment it is meant to police. They drive the
real CLI entrypoint with synthetic `terraform show -json` state.
"""

import json
import os
import subprocess
import sys

import pytest

SCRIPT = os.path.join(
    os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
    "scripts",
    "assert_managed.py",
)

SECRET_ARN = "arn:aws:secretsmanager:eu-west-2:123456789012:secret:prod-app-db-abc"
DB_ADDRESS = "prod-app-db.abcdef.eu-west-2.rds.amazonaws.com"
CACHE_ADDRESS = "prod-app-cache.abcdef.0001.euw2.cache.amazonaws.com"


def run(resources):
    state = {"values": {"root_module": {"resources": resources}}}
    return subprocess.run(
        [sys.executable, SCRIPT],
        input=json.dumps(state),
        capture_output=True,
        text=True,
    )


def task_definition(containers):
    return {
        "type": "aws_ecs_task_definition",
        "name": "app_td",
        "values": {"container_definitions": json.dumps(containers)},
    }


def container(environment=None, secrets=None, image="myapp:latest"):
    return {
        "name": "app",
        "image": image,
        "environment": [
            {"name": k, "value": v} for k, v in (environment or {}).items()
        ],
        "secrets": secrets or [],
    }


def db_instance():
    return {
        "type": "aws_db_instance",
        "name": "db",
        "values": {
            "identifier": "prod-app-db",
            "engine": "postgres",
            "instance_class": "db.t3.micro",
            "address": DB_ADDRESS,
        },
    }


def db_secret():
    return {
        "type": "aws_secretsmanager_secret",
        "name": "db_secret",
        "values": {"arn": SECRET_ARN},
    }


def db_credentials():
    return [
        {"name": "DB_USERNAME", "valueFrom": f"{SECRET_ARN}:username::"},
        {"name": "DB_PASSWORD", "valueFrom": f"{SECRET_ARN}:password::"},
    ]


def cache_cluster():
    return {
        "type": "aws_elasticache_cluster",
        "name": "cache",
        "values": {
            "cluster_id": "prod-app-cache",
            "engine": "redis",
            "node_type": "cache.t3.micro",
            "cache_nodes": [{"address": CACHE_ADDRESS}],
        },
    }


def healthy_rds_stack(**overrides):
    return [
        db_instance(),
        db_secret(),
        task_definition(
            [
                container(
                    environment=overrides.get("environment", {"DB_HOST": DB_ADDRESS}),
                    secrets=overrides.get("secrets", db_credentials()),
                )
            ]
        ),
    ]


def test_no_managed_resources_is_a_no_op():
    result = run([task_definition([container()])])

    assert result.returncode == 0
    assert "nothing to assert" in result.stdout


def test_rds_substitution_passes():
    result = run(healthy_rds_stack())

    assert result.returncode == 0, result.stderr
    assert "RDS substitution assertions passed" in result.stdout


@pytest.mark.parametrize("image", ["postgres:16", "mysql:8", "mariadb:10-focal"])
def test_database_running_as_a_container_fails(image):
    # The substitution check an HTTP health probe cannot make: an app talking to
    # a postgres sidecar is just as healthy as one talking to RDS.
    resources = healthy_rds_stack()
    resources.append(task_definition([container(image=image)]))

    result = run(resources)

    assert result.returncode == 1
    assert "not substituted" in result.stderr


def test_rds_endpoint_not_injected_fails():
    result = run(healthy_rds_stack(environment={"DB_HOST": "db"}))

    assert result.returncode == 1
    assert "endpoint injection did not reach" in result.stderr


def test_rds_credentials_not_from_secrets_manager_fails():
    result = run(healthy_rds_stack(secrets=[]))

    assert result.returncode == 1
    assert "Secrets Manager" in result.stderr


def test_plaintext_database_password_warns_but_passes():
    # A compose file may legitimately point *_PASSWORD at a file path, so this
    # is reported rather than failing an otherwise good deployment.
    result = run(
        healthy_rds_stack(
            environment={"DB_HOST": DB_ADDRESS, "DB_PASSWORD": "hunter2"},
        )
    )

    assert result.returncode == 0, result.stderr
    assert "[warn] plaintext" in result.stdout


def test_elasticache_substitution_passes():
    result = run(
        [
            cache_cluster(),
            task_definition(
                [container(environment={"REDIS_URL": f"redis://{CACHE_ADDRESS}:6379"})]
            ),
        ]
    )

    assert result.returncode == 0, result.stderr
    assert "ElastiCache substitution assertions passed" in result.stdout


def test_redis_running_as_a_container_fails():
    result = run(
        [
            cache_cluster(),
            task_definition(
                [
                    container(
                        environment={"REDIS_URL": f"redis://{CACHE_ADDRESS}:6379"}
                    ),
                    container(image="redis:7"),
                ]
            ),
        ]
    )

    assert result.returncode == 1
    assert "not substituted" in result.stderr


def test_cache_endpoint_not_injected_fails():
    result = run(
        [
            cache_cluster(),
            task_definition(
                [container(environment={"REDIS_URL": "redis://cache:6379"})]
            ),
        ]
    )

    assert result.returncode == 1
    assert "endpoint injection did not reach" in result.stderr


def test_unresolved_address_fails():
    # A reference that never resolved means we are asserting against the plan,
    # not against what actually deployed.
    resources = healthy_rds_stack()
    resources[0]["values"]["address"] = "${aws_db_instance.db.address}"

    result = run(resources)

    assert result.returncode == 1
    assert "no resolved address" in result.stderr


def test_s3_substitution_passes():
    result = run(
        [
            {
                "type": "aws_s3_bucket",
                "name": "blobs",
                "values": {
                    "bucket": "prod-app-blobs",
                    "bucket_domain_name": "prod-app-blobs.s3.amazonaws.com",
                },
            },
            {
                "type": "aws_iam_role_policy",
                "name": "blobs_policy",
                "values": {
                    "name": "prod-app-blobs-policy",
                    "policy": '{"Statement":[{"Action":["s3:*"]}]}',
                },
            },
            task_definition([container(environment={"BUCKET_NAME": "prod-app-blobs"})]),
        ]
    )

    assert result.returncode == 0, result.stderr
    assert "S3 substitution assertions passed" in result.stdout


def test_all_three_substitutions_reported_together():
    resources = healthy_rds_stack(
        environment={"DB_HOST": DB_ADDRESS, "REDIS_URL": CACHE_ADDRESS}
    )
    resources.append(cache_cluster())

    result = run(resources)

    assert result.returncode == 0, result.stderr
    assert "RDS, ElastiCache substitution assertions passed" in result.stdout


ALB_HOST = "prodstack-alb-123.eu-west-2.elb.amazonaws.com"
WAF_ARN = "arn:aws:wafv2:us-east-1:123456789012:global/webacl/prodstack-waf/abc"


def cloudfront(origin=ALB_HOST, web_acl=WAF_ARN, enabled=True):
    return {
        "type": "aws_cloudfront_distribution",
        "name": "web_cdn",
        "values": {
            "enabled": enabled,
            "comment": "CDN for web",
            "domain_name": "d111.cloudfront.net",
            "origin": [{"domain_name": origin, "origin_id": "ALB"}],
            "web_acl_id": web_acl,
        },
    }


def web_acl(arn=WAF_ARN):
    return {
        "type": "aws_wafv2_web_acl",
        "name": "web_waf",
        "values": {"name": "prodstack-waf", "scope": "CLOUDFRONT", "arn": arn},
    }


def test_cdn_assertions_pass():
    result = run([cloudfront(), web_acl()])

    assert result.returncode == 0, result.stderr
    assert "CloudFront substitution assertions passed" in result.stdout


def test_a_waf_outside_us_east_1_fails():
    # CLOUDFRONT-scoped ACLs only exist in us-east-1. This is what proves the
    # aliased provider works while the rest of the stack is in eu-west-2.
    wrong = WAF_ARN.replace("us-east-1", "eu-west-2")

    result = run([cloudfront(web_acl=wrong), web_acl(arn=wrong)])

    assert result.returncode == 1
    assert "not in us-east-1" in result.stderr


def test_a_distribution_without_a_waf_fails():
    result = run([cloudfront()])

    assert result.returncode == 1
    assert "unprotected" in result.stderr


def test_an_origin_pointing_somewhere_else_fails():
    # The origin used to be derived by string-splitting the load balancer ARN,
    # which produced something that was not a hostname at all.
    result = run([cloudfront(origin="app"), web_acl()])

    assert result.returncode == 1
    assert "not the load balancer" in result.stderr


def schedule_rule(expression="cron(0 2 * * ? *)"):
    return {
        "type": "aws_cloudwatch_event_rule",
        "name": "cleanup_rule",
        "values": {
            "name": "prodstack-cleanup",
            "schedule_expression": expression,
            "state": "ENABLED",
        },
    }


def schedule_target(task_arn="arn:aws:ecs:eu-west-2:1:task-definition/cleanup:1"):
    return {
        "type": "aws_cloudwatch_event_target",
        "name": "cleanup_target",
        "values": {"ecs_target": [{"task_definition_arn": task_arn}]},
    }


def test_schedule_assertions_pass():
    result = run([schedule_rule(), schedule_target()])

    assert result.returncode == 0, result.stderr
    assert "EventBridge substitution assertions passed" in result.stdout


def test_a_schedule_with_nothing_to_run_fails():
    result = run([schedule_rule()])

    assert result.returncode == 1
    assert "nothing is wired to run" in result.stderr


def test_a_scheduled_task_also_running_as_a_service_fails():
    task = "arn:aws:ecs:eu-west-2:1:task-definition/cleanup:1"
    service = {
        "type": "aws_ecs_service",
        "name": "cleanup_service",
        "values": {"name": "cleanup", "task_definition": task},
    }

    result = run([schedule_rule(), schedule_target(task), service])

    assert result.returncode == 1
    assert "running continuously" in result.stderr


def scaling_target(low=2, high=10):
    return {
        "type": "aws_appautoscaling_target",
        "name": "web_asg_target",
        "values": {
            "min_capacity": low,
            "max_capacity": high,
            "resource_id": "service/prodstack/web",
        },
    }


def scaling_policy():
    return {
        "type": "aws_appautoscaling_policy",
        "name": "web_cpu_scaling",
        "values": {"name": "cpu"},
    }


def test_scaling_assertions_pass():
    result = run([scaling_target(), scaling_policy()])

    assert result.returncode == 0, result.stderr
    assert "scaling substitution assertions passed" in result.stdout


def test_a_scaling_target_that_cannot_scale_fails():
    result = run([scaling_target(low=1, high=1), scaling_policy()])

    assert result.returncode == 1
    assert "cannot scale" in result.stderr


def test_a_scaling_target_with_no_policy_fails():
    result = run([scaling_target()])

    assert result.returncode == 1
    assert "no policy decides when to scale" in result.stderr


NAMESPACE = "prod-webapi.internal"
API_REGISTRY = "arn:aws:servicediscovery:eu-west-2:1:service/srv-api"


def namespace():
    return {
        "type": "aws_service_discovery_private_dns_namespace",
        "name": "app",
        "values": {"name": NAMESPACE, "id": "ns-1"},
    }


def discovery(name="api", arn=API_REGISTRY):
    return {
        "type": "aws_service_discovery_service",
        "name": f"{name}_discovery",
        "values": {"name": name, "arn": arn},
    }


def ecs_service(name="api", registry=API_REGISTRY):
    values = {"name": name, "task_definition": f"arn:...:{name}:1"}
    if registry:
        values["service_registries"] = [{"registry_arn": registry}]
    return {"type": "aws_ecs_service", "name": f"{name}_service", "values": values}


def test_service_discovery_assertions_pass():
    result = run(
        [
            namespace(),
            discovery(),
            ecs_service(),
            task_definition(
                [container(environment={"API_URL": f"http://api.{NAMESPACE}:80"})]
            ),
        ]
    )

    assert result.returncode == 0, result.stderr
    assert "Cloud Map substitution assertions passed" in result.stdout
    assert "API_URL points at a registered service" in result.stdout


def test_a_registration_with_no_service_behind_it_fails():
    # A DNS record that nothing answers is worse than no record at all.
    result = run([namespace(), discovery(), ecs_service(registry=None)])

    assert result.returncode == 1
    assert "nothing will ever answer" in result.stderr


def test_registering_without_a_namespace_fails():
    result = run([discovery(), ecs_service()])

    assert result.returncode == 1
    assert "no namespace" in result.stderr


def test_an_example_where_nothing_refers_to_a_sibling_still_passes():
    result = run([namespace(), discovery(), ecs_service()])

    assert result.returncode == 0, result.stderr
    assert "no service refers to another by name" in result.stdout
