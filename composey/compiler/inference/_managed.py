"""Managed services inference (RDS, ElastiCache, S3).

Handles inference of cloud-native managed services that substitute for
container-based services.
"""

import json
from typing import Callable

from composey.constants import (
    DATABASE_DEFAULT_USERNAME,
    DB_INSTANCE_CLASSES,
    CACHE_NODE_TYPES,
    DefaultPorts,
)
from composey.models.aws import (
    AWSResources,
    DbInstance,
    DbSubnetGroup,
    ElastiCacheCluster,
    ElastiCacheSubnetGroup,
    RandomId,
    RandomPassword,
    S3Bucket,
    SecretsManagerSecret,
    SecretsManagerSecretVersion,
)
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application as SemanticApp
from composey.models.semantic import Connection

from ._connectivity import security_group_ids


def infer_managed_services(
    resources: AWSResources,
    app: SemanticApp,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    discard: bool,
) -> dict[str, Connection]:
    """Infer managed services (RDS, ElastiCache, S3) and return their connections.

    Returns a mapping of service name to Connection for use in wiring.
    """
    from ._common import namespace_for

    namespace = namespace_for(env.name, app.name)
    connections: dict[str, Connection] = {}

    for service in app.services:
        if service.capability == "database":
            connection = _infer_database(
                resources, service, env, get_name, tags, discard, namespace
            )
            if connection:
                connections[service.name] = connection

        elif service.capability == "cache":
            connection = _infer_cache(
                resources, service, env, get_name, tags, namespace
            )
            if connection:
                connections[service.name] = connection

        elif service.capability == "object-storage":
            connection = _infer_object_storage(
                resources, service, env, get_name, tags, discard
            )
            if connection:
                connections[service.name] = connection

    return connections


def _infer_database(
    resources: AWSResources,
    service,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    discard: bool,
    namespace: str,
) -> Connection | None:
    """Infer RDS database resources."""
    # Determine engine
    engine = "postgres"
    if "mysql" in service.image.lower():
        engine = "mysql"
    elif "mariadb" in service.image.lower():
        engine = "mariadb"

    db_username = DATABASE_DEFAULT_USERNAME

    # Create random master password
    password_key = f"{service.name}_password"
    resources.random_password[password_key] = RandomPassword(length=20)

    # Store credentials in Secrets Manager
    db_secret_key = f"{service.name}_db_secret"
    resources.aws_secretsmanager_secret[db_secret_key] = SecretsManagerSecret(
        name=get_name(f"{service.name}-credentials"),
        description=f"Credentials for {service.name} RDS",
        tags=tags,
    )

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

    # Create subnet group
    sng_key = f"{service.name}_sng"
    resources.aws_db_subnet_group[sng_key] = DbSubnetGroup(
        name=get_name(f"{service.name}-sng"),
        subnet_ids=env.private_subnets,
        tags=tags,
    )

    # Create unique snapshot identifier if retaining
    if not discard:
        resources.random_id[f"{service.name}_snapshot"] = RandomId()

    # Create RDS instance
    db_key = f"{service.name}_db"
    resources.aws_db_instance[db_key] = DbInstance(
        identifier=get_name(service.name),
        engine=engine,
        db_name=service.database_name,
        instance_class=DB_INSTANCE_CLASSES.get(
            service.size, DB_INSTANCE_CLASSES["small"]
        ),
        allocated_storage=20,
        db_subnet_group_name=f"${{aws_db_subnet_group.{sng_key}.name}}",
        vpc_security_group_ids=security_group_ids(service.network_isolation_segments),
        skip_final_snapshot=discard,
        final_snapshot_identifier=None
        if discard
        else (
            f"{get_name(service.name)}-final-${{random_id.{service.name}_snapshot.hex}}"
        ),
        publicly_accessible=False,
        username=db_username,
        password=f"${{random_password.{password_key}.result}}",
        tags=tags,
    )

    return Connection(
        host=f"${{aws_db_instance.{db_key}.address}}",
        port=DefaultPorts.for_database(engine),
        username=db_username,
        password=f"${{random_password.{password_key}.result}}",
        database=service.database_name,
    )


def _infer_cache(
    resources: AWSResources,
    service,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    namespace: str,
) -> Connection | None:
    """Infer ElastiCache Redis resources."""
    # Create subnet group
    sng_key = f"{service.name}_sng"
    resources.aws_elasticache_subnet_group[sng_key] = ElastiCacheSubnetGroup(
        name=get_name(f"{service.name}-sng"),
        subnet_ids=env.private_subnets,
        tags=tags,
    )

    # Create ElastiCache cluster
    cache_key = f"{service.name}_cache"
    resources.aws_elasticache_cluster[cache_key] = ElastiCacheCluster(
        cluster_id=get_name(service.name),
        engine="redis",
        node_type=CACHE_NODE_TYPES.get(service.size, CACHE_NODE_TYPES["small"]),
        num_cache_nodes=1,
        subnet_group_name=f"${{aws_elasticache_subnet_group.{sng_key}.name}}",
        security_group_ids=security_group_ids(service.network_isolation_segments),
        tags=tags,
    )

    return Connection(
        host=f"${{aws_elasticache_cluster.{cache_key}.cache_nodes[0].address}}",
        port=DefaultPorts.REDIS,
    )


def _infer_object_storage(
    resources: AWSResources,
    service,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    discard: bool,
) -> Connection | None:
    """Infer S3 bucket resources (substitutes for Minio)."""
    bucket_key = f"{service.name}_bucket"
    resources.aws_s3_bucket[bucket_key] = S3Bucket(
        bucket=get_name(service.name).lower().replace("_", "-")[:63].rstrip("-"),
        force_destroy=discard,
        tags=tags,
    )

    return Connection(
        host=f"${{aws_s3_bucket.{bucket_key}.bucket_domain_name}}",
        name=f"${{aws_s3_bucket.{bucket_key}.id}}",
        addressed_by="name",
        port=None,
    )
