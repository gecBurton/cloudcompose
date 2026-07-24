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
