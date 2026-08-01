"""GCP resource inference for Composey.

Translates cloud-agnostic semantic model to GCP-specific resources.
Cloud Run is the primary compute platform, which differs significantly
from ECS Fargate and Azure Container Apps.
"""

from typing import Callable

from composey.models.environment import GcpEnvironment
from composey.models.gcp import (
    CloudRunService,
    CloudSqlDatabase,
    CloudSqlInstance,
    GcpResources,
    RedisInstance,
    StorageBucket,
    VpcConnector,
)
from composey.models.semantic import Application as SemanticApp
from composey.models.semantic import Connection


def infer(app: SemanticApp, env: GcpEnvironment) -> GcpResources:
    """Infer GCP resources from a semantic application model.

    This is the main entry point for GCP compilation. It orchestrates
    the inference of all GCP resources needed to deploy the application.

    Key differences from AWS/Azure:
    - Cloud Run is fully serverless (scales to zero by default)
    - Built-in HTTPS (no separate load balancer for simple cases)
    - Request-based concurrency (not instance-based)
    - VPC access requires VPC connector

    Args:
        app: The semantic application model
        env: The target GCP environment configuration

    Returns:
        GcpResources containing all inferred resources
    """
    resources = GcpResources()

    # Helper for resource naming
    def get_name(resource_name: str) -> str:
        return f"{env.name}-{app.name}-{resource_name}"

    tags = env.tags if env.tags else None

    # Step 1: Create VPC connector if needed
    vpc_connector_name = _infer_vpc_connector(resources, app, env, get_name, tags)

    # Step 2: Create Cloud SQL instance if needed
    connections = _infer_databases(resources, app, env, get_name, tags)

    # Step 3: Create Memorystore Redis if needed
    cache_connections = _infer_caches(resources, app, env, get_name, tags)
    connections.update(cache_connections)

    # Step 4: Create Cloud Storage buckets if needed
    storage_connections = _infer_storage(resources, app, env, get_name, tags)
    connections.update(storage_connections)

    # Step 5: Create Cloud Run services
    _infer_cloud_run_services(
        resources, app, env, get_name, tags, vpc_connector_name, connections
    )

    # Step 6: Create load balancer for custom domains / CDN
    _infer_load_balancer(resources, app, env, get_name, tags)

    return resources


def _infer_vpc_connector(
    resources: GcpResources,
    app: SemanticApp,
    env: GcpEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> str | None:
    """Create VPC connector for private networking if needed."""
    # Only create if we have databases that need private access
    needs_private = any(s.capability == "database" for s in app.services)

    if not needs_private:
        return None

    connector_name = get_name("vpc-connector")
    resources.google_vpc_access_connector["main"] = VpcConnector(
        name=connector_name,
        project_id=env.project_id,
        region=env.region,
        network=env.vpc_id or "default",
    )

    return connector_name


def _infer_databases(
    resources: GcpResources,
    app: SemanticApp,
    env: GcpEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> dict[str, Connection]:
    """Infer Cloud SQL databases."""
    from composey.constants import DATABASE_DEFAULT_USERNAME

    connections: dict[str, Connection] = {}

    db_services = [s for s in app.services if s.capability == "database"]

    if not db_services:
        return connections

    # Create one Cloud SQL instance for all databases
    instance_name = get_name("db")
    instance_key = "main"

    resources.random_password["db_root"] = {"length": 20}

    resources.google_sql_database_instance[instance_key] = CloudSqlInstance(
        name=instance_name,
        project_id=env.project_id,
        region=env.region,
        database_version="POSTGRES_14",
        tier="db-f1-micro",
        root_password="${random_password.db_root.result}",
    )

    for service in db_services:
        db_key = f"{service.name}_db"
        resources.google_sql_database[db_key] = CloudSqlDatabase(
            name=service.database_name or service.name,
            instance=f"${{google_sql_database_instance.{instance_key}.name}}",
            project_id=env.project_id,
        )

        connections[service.name] = Connection(
            host=f"${{google_sql_database_instance.{instance_key}.public_ip_address}}",
            port=5432,
            username=DATABASE_DEFAULT_USERNAME,
            password="${random_password.db_root.result}",
            database=service.database_name or service.name,
        )

    return connections


def _infer_caches(
    resources: GcpResources,
    app: SemanticApp,
    env: GcpEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> dict[str, Connection]:
    """Infer Memorystore Redis instances."""
    from composey.constants import DefaultPorts

    connections: dict[str, Connection] = {}

    cache_services = [s for s in app.services if s.capability == "cache"]

    for service in cache_services:
        cache_key = f"{service.name}_redis"

        resources.google_redis_instance[cache_key] = RedisInstance(
            name=get_name(service.name),
            project_id=env.project_id,
            region=env.region,
            tier="BASIC",
            memory_size_gb=1,
        )

        connections[service.name] = Connection(
            host=f"${{google_redis_instance.{cache_key}.host}}",
            port=DefaultPorts.REDIS,
        )

    return connections


def _infer_storage(
    resources: GcpResources,
    app: SemanticApp,
    env: GcpEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> dict[str, Connection]:
    """Infer Cloud Storage buckets."""
    connections: dict[str, Connection] = {}

    storage_services = [s for s in app.services if s.capability == "object-storage"]

    for service in storage_services:
        bucket_key = f"{service.name}_bucket"
        bucket_name = get_name(service.name).replace("_", "-").lower()

        resources.google_storage_bucket[bucket_key] = StorageBucket(
            name=bucket_name,
            project_id=env.project_id,
            location="US",
            force_destroy=not env.retain_data_on_destroy,
        )

        connections[service.name] = Connection(
            host=f"${{google_storage_bucket.{bucket_key}.url}}",
            name=f"${{google_storage_bucket.{bucket_key}.name}}",
            addressed_by="name",
        )

    return connections


def _infer_cloud_run_services(
    resources: GcpResources,
    app: SemanticApp,
    env: GcpEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    vpc_connector_name: str | None,
    connections: dict[str, Connection],
) -> None:
    """Create Cloud Run services.

    Key differences from ECS/Container Apps:
    - Built-in HTTPS (no separate load balancer needed)
    - Scale to zero by default
    - Request-based concurrency (up to 1000 per instance)
    - Built-in service discovery within project
    """
    for service in app.services:
        if service.capability != "container":
            continue

        service_key = service.name

        # Build container spec
        container = {
            "image": service.image,
            "resources": {
                "limits": {
                    "cpu": _get_cpu_limit(service),
                    "memory": _get_memory_limit(service),
                }
            },
        }

        if service.command:
            container["command"] = service.command

        # Environment variables
        env_vars = []
        for key, value in service.env.items():
            env_vars.append({"name": key, "value": value})

        # Add connection strings
        for target_name, conn in connections.items():
            # Check if this service depends on the target
            if any(
                r.client == service.name and r.server == target_name
                for r in app.relationships
            ):
                env_vars.append(
                    {
                        "name": f"{target_name.upper()}_URL",
                        "value": _build_connection_url(conn),
                    }
                )

        if env_vars:
            container["env"] = env_vars

        # Build template
        template = {
            "spec": {
                "containers": [container],
                "service_account_name": env.service_account_email
                or f"{env.project_id}-compute@developer.gserviceaccount.com",
            }
        }

        # Scaling configuration
        # Cloud Run scales to zero by default (unlike ECS)
        min_scale = service.min_scale
        max_scale = service.max_scale

        # Container Apps-style concurrency (can be up to 1000)
        _ = service.auto_scaling  # Mark as used

        template["metadata"] = {
            "annotations": {
                "autoscaling.knative.dev/minScale": str(min_scale),
                "autoscaling.knative.dev/maxScale": str(max_scale),
                "run.googleapis.com/cpu-throttling": "true",  # Scale to zero
                "run.googleapis.com/execution-environment": "gen2",
            }
        }

        # VPC access for private networking
        if vpc_connector_name:
            template["metadata"]["annotations"][
                "run.googleapis.com/vpc-access-connector"
            ] = vpc_connector_name
            template["metadata"]["annotations"][
                "run.googleapis.com/vpc-access-egress"
            ] = "all-traffic"

        # Traffic configuration
        traffic = [{"percent": 100, "latest_revision": True}]

        # Ingress settings
        ingress = "all"  # Allow all traffic
        if service.ingress is None:
            ingress = "internal"  # Private service

        resources.google_cloud_run_service[service_key] = CloudRunService(
            name=get_name(service.name),
            location=env.region,
            project_id=env.project_id,
            template=template,
            traffic=traffic,
            ingress=ingress,
        )


def _infer_load_balancer(
    resources: GcpResources,
    app: SemanticApp,
    env: GcpEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> None:
    """Create load balancer for custom domains / CDN.

    Unlike AWS, Cloud Run services have built-in HTTPS URLs, so we only
    need a load balancer for:
    - Custom domains
    - CDN (Cloud CDN)
    - Global load balancing
    """
    # TODO: Implement if cdn_enabled or custom domain needed
    # For now, Cloud Run's built-in URL is sufficient
    pass


def _get_cpu_limit(service) -> str:
    """Convert service size to Cloud Run CPU limit."""
    if service.cpu:
        return f"{service.cpu}m"

    size_map = {
        "small": "1000m",  # 1 vCPU
        "medium": "2000m",  # 2 vCPU
        "large": "4000m",  # 4 vCPU
    }
    return size_map.get(service.size, "1000m")


def _get_memory_limit(service) -> str:
    """Convert service size to Cloud Run memory limit."""
    if service.memory:
        return f"{service.memory}Mi"

    size_map = {
        "small": "512Mi",
        "medium": "1Gi",
        "large": "2Gi",
    }
    return size_map.get(service.size, "512Mi")


def _build_connection_url(conn: Connection) -> str:
    """Build connection URL from Connection object."""
    if conn.database:
        return f"postgresql://{conn.username}:{conn.password}@{conn.host}:{conn.port}/{conn.database}"
    return f"{conn.host}:{conn.port}"
