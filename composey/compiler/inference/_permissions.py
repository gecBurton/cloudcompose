"""Permissions and connection wiring.

Handles IAM permission grants and environment variable resolution
for service connections.
"""

import json
from typing import Callable

from composey.compiler.connections import resolve_value
from composey.constants import IAM_POLICY_VERSION
from composey.models.aws import (
    AWSResources,
    IamRolePolicy,
    SecretsManagerSecret,
    SecretsManagerSecretVersion,
)
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application as SemanticApp
from composey.models.semantic import Connection

from ._common import safe_terraform_identifier


def infer_permissions_and_wiring(
    resources: AWSResources,
    app: SemanticApp,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    connections: dict[str, Connection],
) -> None:
    """Wire up connections and grant IAM permissions.

    For each container service, resolve environment variables that reference
    managed services, update task definitions, and grant necessary IAM permissions.
    """
    for service in app.services:
        if service.capability != "container":
            continue

        task_def_key = f"{service.name}_td"
        if task_def_key not in resources.aws_ecs_task_definition:
            continue

        task_def = resources.aws_ecs_task_definition[task_def_key]
        container_defs = json.loads(task_def.container_definitions)
        container = container_defs[0]

        exec_role_key = f"{service.name}_exec_role"

        # Resolve environment variables and track references
        environment: list[dict] = []
        referenced: set[str] = set()

        for entry in container["environment"]:
            resolved = resolve_value(entry["value"], connections)
            if resolved.service is not None:
                referenced.add(resolved.service)

            if not resolved.confidential:
                environment.append({**entry, "value": resolved.value})
                continue

            # Confidential values go to Secrets Manager
            _store_confidential_value(
                resources,
                service.name,
                entry["name"],
                resolved.value,
                resolved.service,
                app.name,
                get_name,
                tags=env.tags if env.tags else None,
                exec_role_key=exec_role_key,
                container=container,
            )

        container["environment"] = environment

        # Grant permissions based on references
        for server_name in sorted(referenced):
            server = next((s for s in app.services if s.name == server_name), None)
            if server is None:
                continue

            if server.capability == "database":
                _grant_database_permissions(
                    resources,
                    service.name,
                    server.name,
                    app,
                    get_name,
                    env.tags if env.tags else None,
                    exec_role_key,
                    container,
                )
            elif server.capability == "object-storage":
                _grant_s3_permissions(
                    resources,
                    service.name,
                    server.name,
                    get_name,
                    env.tags if env.tags else None,
                )

        task_def.container_definitions = json.dumps(container_defs)


def _store_confidential_value(
    resources: AWSResources,
    service_name: str,
    var_name: str,
    value: str,
    referenced_service: str | None,
    app_name: str,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    exec_role_key: str,
    container: dict,
) -> None:
    """Store a confidential value in Secrets Manager and wire it to the container."""
    url_key = f"{service_name}_{safe_terraform_identifier(var_name).lower()}_url"

    resources.aws_secretsmanager_secret[url_key] = SecretsManagerSecret(
        name=get_name(f"{service_name}-{var_name.lower()}"),
        description=(
            f"{var_name} for {service_name}, including credentials "
            f"for {referenced_service}"
        ),
        tags=tags,
    )

    resources.aws_secretsmanager_secret_version[f"{url_key}_v1"] = (
        SecretsManagerSecretVersion(
            secret_id=f"${{aws_secretsmanager_secret.{url_key}.id}}",
            secret_string=value,
            # Deliberately no ignore_changes - rotated passwords must reach clients
        )
    )

    container["secrets"].append(
        {
            "name": var_name,
            "valueFrom": f"${{aws_secretsmanager_secret.{url_key}.arn}}",
        }
    )

    # Grant read access
    resources.aws_iam_role_policy[f"{service_name}_{url_key}_policy"] = IamRolePolicy(
        name=get_name(f"{service_name}-{var_name.lower()}-policy"),
        role=f"${{aws_iam_role.{exec_role_key}.name}}",
        policy=json.dumps(
            {
                "Version": IAM_POLICY_VERSION,
                "Statement": [
                    {
                        "Effect": "Allow",
                        "Action": ["secretsmanager:GetSecretValue"],
                        "Resource": [f"${{aws_secretsmanager_secret.{url_key}.arn}}"],
                    }
                ],
            }
        ),
    )


def _grant_database_permissions(
    resources: AWSResources,
    client_name: str,
    server_name: str,
    app: SemanticApp,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    exec_role_key: str,
    container: dict,
) -> None:
    """Grant IAM permissions for database access and wire credentials."""
    db_secret_key = f"{server_name}_db_secret"

    # Add credential references to container secrets
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

    # Grant IAM permission to read database credentials
    resources.aws_iam_role_policy[f"{client_name}_to_{server_name}_rds_secret"] = (
        IamRolePolicy(
            name=get_name(f"{client_name}-{server_name}-rds-secret"),
            role=f"${{aws_iam_role.{exec_role_key}.name}}",
            policy=json.dumps(
                {
                    "Version": IAM_POLICY_VERSION,
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
    )


def _grant_s3_permissions(
    resources: AWSResources,
    client_name: str,
    server_name: str,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> None:
    """Grant IAM permissions for S3 bucket access."""
    bucket_key = f"{server_name}_bucket"
    task_role_key = f"{client_name}_task_role"

    resources.aws_iam_role_policy[f"{client_name}_to_{server_name}_s3_policy"] = (
        IamRolePolicy(
            name=get_name(f"{client_name}-{server_name}-s3-policy"),
            role=f"${{aws_iam_role.{task_role_key}.name}}",
            policy=json.dumps(
                {
                    "Version": IAM_POLICY_VERSION,
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
    )
