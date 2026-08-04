"""Azure resource inference for Composey.

Translates cloud-agnostic semantic model to Azure-specific resources.
Azure Container Apps is a higher-level service than ECS Fargate, so this
module makes opinionated choices that fit Azure's model.
"""

import warnings
from typing import Callable

from composey.compiler.inference.azure.naming import (
    container_registry_name,
    key_vault_name,
    storage_account_name,
)
from composey.models.aws import RandomPassword
from composey.models.azure import (
    AzureResources,
    ContainerApp,
    ContainerAppJob,
    ContainerRegistry,
    KeyVault,
    MySQLFlexibleDatabase,
    MySQLFlexibleServer,
    PostgreSQLFlexibleDatabase,
    PostgreSQLFlexibleServer,
    PrivateDnsZone,
    PrivateDnsZoneVirtualNetworkLink,
    StorageAccount,
    StorageContainer,
)
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application as SemanticApp
from composey.models.semantic import Connection


def infer(app: SemanticApp, env: AzureEnvironment) -> AzureResources:
    """Infer Azure resources from a semantic application model.

    This is the main entry point for Azure compilation. It orchestrates
    the inference of all Azure resources needed to deploy the application.

    Args:
        app: The semantic application model
        env: The target Azure environment configuration

    Returns:
        AzureResources containing all inferred resources
    """
    resources = AzureResources()

    # Helper for resource naming
    def get_name(resource_name: str) -> str:
        return f"{env.name}-{app.name}-{resource_name}"

    tags = env.tags if env.tags else None

    # The Container Apps Environment is deliberately not inferred: it is
    # platform-owned and referenced through a data source. See generator_azure.py.

    # Step 1: Create managed identity (or use existing)
    identity_id = _infer_managed_identity(resources, app, env, get_name, tags)

    # Step 3: Create Key Vault for secrets
    _infer_key_vault(resources, app, env, get_name, tags, identity_id)

    # Step 4: Create container registry (if building from source)
    _infer_container_registry(resources, app, env, get_name, tags)

    # Step 5: Infer database resources
    connections = _infer_databases(resources, app, env, get_name, tags)

    # Step 6: Infer cache resources
    cache_connections = _infer_caches(resources, app, env, get_name, tags)
    connections.update(cache_connections)

    # Step 7: Infer storage resources
    storage_connections = _infer_storage(resources, app, env, get_name, tags)
    connections.update(storage_connections)

    # Step 8: Infer container apps
    _infer_container_apps(resources, app, env, get_name, tags, identity_id, connections)

    # Step 9: Scheduled services run as Jobs, not as always-on Container Apps
    _infer_scheduled_jobs(resources, app, env, get_name, tags, identity_id, connections)

    # Step 10: Infer CDN for services with cdn_enabled
    _infer_cdn(resources, app, env, get_name, tags)

    return resources


def _infer_managed_identity(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> str:
    """Create or reference managed identity.

    Returns the identity resource ID for use in other resources.
    """
    if env.user_assigned_identity_id:
        # Use existing identity
        return env.user_assigned_identity_id

    # Create system-assigned identity via Container Apps (handled in ContainerApp)
    # For now, return empty - Container Apps will use system-assigned
    return ""


def _infer_key_vault(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    identity_id: str,
) -> None:
    """Create Key Vault for secrets."""
    kv_key = "main"
    resources.azurerm_key_vault[kv_key] = KeyVault(
        name=key_vault_name(env.name, app.name),
        resource_group_name=env.name,
        location=env.region,
        tenant_id="${data.azurerm_client_config.current.tenant_id}",
        tags=tags,
    )


def _infer_container_registry(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> None:
    """Create Azure Container Registry if any service builds from source."""
    needs_registry = any(
        s.build_context for s in app.services if s.capability == "container"
    )

    if not needs_registry:
        return

    registry_name = env.container_registry_name or container_registry_name(
        env.name, app.name
    )

    resources.azurerm_container_registry["main"] = ContainerRegistry(
        name=registry_name,
        resource_group_name=env.name,
        location=env.region,
        sku="Standard",
        admin_enabled=True,  # Required for Container Apps to pull
        tags=tags,
    )


def _private_networking(
    resources: AzureResources,
    env: AzureEnvironment,
    app: SemanticApp,
    engine: str,
    subnet_id: str | None,
    tags: dict[str, str] | None,
) -> dict:
    """
    Arguments placing a Flexible Server on the environment's private network.

    Azure will not create a server on a delegated subnet without a private DNS
    zone (EmptyPrivateDnsZoneArmResourceId), and the zone has to be linked to
    the VNet before the server exists. Environments predating those subnets
    have no delegated subnet to use, so the server falls back to public network
    access rather than failing to compile.
    """
    if not subnet_id:
        return {"public_network_access_enabled": True}

    zone_key = f"{engine}"
    suffix = "postgres" if engine == "postgresql" else "mysql"
    zone_name = f"{app.name}-{engine}.{suffix}.database.azure.com"

    resources.azurerm_private_dns_zone[zone_key] = PrivateDnsZone(
        name=zone_name,
        resource_group_name=env.name,
        tags=tags,
    )
    resources.azurerm_private_dns_zone_virtual_network_link[zone_key] = (
        PrivateDnsZoneVirtualNetworkLink(
            name=f"{app.name}-{engine}-link",
            resource_group_name=env.name,
            private_dns_zone_name=f"${{azurerm_private_dns_zone.{zone_key}.name}}",
            virtual_network_id=env.vnet_id,
            tags=tags,
        )
    )

    return {
        "delegated_subnet_id": subnet_id,
        "private_dns_zone_id": f"${{azurerm_private_dns_zone.{zone_key}.id}}",
        "public_network_access_enabled": False,
        "depends_on": [f"azurerm_private_dns_zone_virtual_network_link.{zone_key}"],
    }


def _infer_databases(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> dict[str, Connection]:
    """Infer PostgreSQL and MySQL Flexible Server databases.

    Returns a mapping of service name to Connection for use in wiring.
    """
    from composey.constants import DATABASE_DEFAULT_USERNAME, DefaultPorts

    connections: dict[str, Connection] = {}

    # Separate PostgreSQL and MySQL services
    pg_services = []
    mysql_services = []

    for s in app.services:
        if s.capability != "database":
            continue
        # Determine engine from image name
        image_lower = s.image.lower()
        if "mysql" in image_lower and "postgres" not in image_lower:
            mysql_services.append(s)
        else:
            # Default to PostgreSQL (including postgres, postgresql, pgvector, etc.)
            pg_services.append(s)

    # Create PostgreSQL server if needed
    if pg_services and not env.postgresql_server_id:
        server_name = get_name("pg")
        admin_password_key = "postgres_admin"
        resources.random_password[admin_password_key] = RandomPassword(length=20)

        resources.azurerm_postgresql_flexible_server["main"] = PostgreSQLFlexibleServer(
            name=server_name,
            resource_group_name=env.name,
            location=env.region,
            administrator_login=DATABASE_DEFAULT_USERNAME,
            administrator_password=f"${{random_password.{admin_password_key}.result}}",
            version="14",
            sku_name="B_Standard_B1ms",
            storage_mb=32768,
            tags=tags,
            **_private_networking(
                resources, env, app, "postgresql", env.postgresql_subnet_id, tags
            ),
        )

        for service in pg_services:
            db_key = f"{service.name}_db"
            resources.azurerm_postgresql_flexible_server_database[db_key] = (
                PostgreSQLFlexibleDatabase(
                    name=service.database_name or service.name,
                    server_id="${azurerm_postgresql_flexible_server.main.id}",
                )
            )

            connections[service.name] = Connection(
                host="${azurerm_postgresql_flexible_server.main.fqdn}",
                port=DefaultPorts.POSTGRES,
                username=DATABASE_DEFAULT_USERNAME,
                password=f"${{random_password.{admin_password_key}.result}}",
                database=service.database_name or service.name,
            )

    # Create MySQL server if needed
    if mysql_services:
        server_name = get_name("mysql")
        admin_password_key = "mysql_admin"
        resources.random_password[admin_password_key] = RandomPassword(length=20)

        resources.azurerm_mysql_flexible_server["main"] = MySQLFlexibleServer(
            name=server_name,
            resource_group_name=env.name,
            location=env.region,
            administrator_login=DATABASE_DEFAULT_USERNAME,
            administrator_password=f"${{random_password.{admin_password_key}.result}}",
            version="8.0",
            sku_name="B_Standard_B1ms",
            storage_mb=32768,
            tags=tags,
            **_private_networking(
                resources, env, app, "mysql", env.mysql_subnet_id, tags
            ),
        )

        for service in mysql_services:
            db_key = f"{service.name}_db"
            resources.azurerm_mysql_flexible_database[db_key] = MySQLFlexibleDatabase(
                name=service.database_name or service.name,
                server_id="${azurerm_mysql_flexible_server.main.id}",
            )

            connections[service.name] = Connection(
                host="${azurerm_mysql_flexible_server.main.fqdn}",
                port=DefaultPorts.MYSQL,
                username=DATABASE_DEFAULT_USERNAME,
                password=f"${{random_password.{admin_password_key}.result}}",
                database=service.database_name or service.name,
            )

    return connections


def _infer_caches(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> dict[str, Connection]:
    """Infer Azure Managed Redis instances.

    Returns a mapping of service name to Connection for use in wiring.
    """
    from composey.constants import DefaultPorts
    from composey.models.azure import ManagedRedis

    connections: dict[str, Connection] = {}

    cache_services = [s for s in app.services if s.capability == "cache"]

    for service in cache_services:
        cache_key = f"{service.name}_redis"

        # Balanced tier: the general-purpose Managed Redis family. B0 is the
        # smallest, and cheaper than the Basic C0 it replaces.
        size_sku_map = {
            "small": "Balanced_B0",
            "medium": "Balanced_B1",
            "large": "Balanced_B3",
        }
        sku_name = size_sku_map.get(service.size, "Balanced_B0")

        resources.azurerm_managed_redis[cache_key] = ManagedRedis(
            name=get_name(service.name),
            resource_group_name=env.name,
            location=env.region,
            sku_name=sku_name,
            # A single-node cache is enough for a cache; replication is a
            # deliberate production choice, not a default to pay for.
            high_availability_enabled=False,
            tags=tags,
        )

        # The access key hangs off the nested database, not the cluster. The
        # port does too, but Connection.port is an int, so the well-known
        # Managed Redis port is named directly rather than interpolated.
        db = f"azurerm_managed_redis.{cache_key}.default_database[0]"
        connections[service.name] = Connection(
            host=f"${{azurerm_managed_redis.{cache_key}.hostname}}",
            port=DefaultPorts.AZURE_MANAGED_REDIS,
            password=f"${{{db}.primary_access_key}}",
        )

    return connections


def _infer_storage(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> dict[str, Connection]:
    """Infer Azure Blob Storage accounts and containers.

    Returns a mapping of service name to Connection for use in wiring.
    """
    connections: dict[str, Connection] = {}

    storage_services = [s for s in app.services if s.capability == "object-storage"]

    for service in storage_services:
        # Create storage account
        account_key = f"{service.name}_storage"
        account_name = storage_account_name(env.name, app.name, service.name)

        resources.azurerm_storage_account[account_key] = StorageAccount(
            name=account_name,
            resource_group_name=env.name,
            location=env.region,
            account_tier="Standard",
            account_replication_type="LRS",
            account_kind="StorageV2",
            min_tls_version="TLS1_2",
            https_traffic_only_enabled=True,
            tags=tags,
        )

        # Create default container
        container_key = f"{service.name}_container"
        resources.azurerm_storage_container[container_key] = StorageContainer(
            name=service.name,
            storage_account_name=f"${{azurerm_storage_account.{account_key}.name}}",
            container_access_type="private",
        )

        # Connection uses storage account name and key
        connections[service.name] = Connection(
            host=f"${{azurerm_storage_account.{account_key}.primary_blob_endpoint}}",
            name=f"${{azurerm_storage_account.{account_key}.name}}",
            addressed_by="name",
        )

    return connections


def _cron_expression(schedule) -> str:
    """
    Render a cloud-neutral schedule as the standard 5-field cron Azure wants.

    Azure has no rate dialect, so an interval is expressed as the cron that
    means the same thing. Intervals that cron cannot express — anything that
    does not divide its unit evenly, like every 7 hours — are rejected rather
    than silently rounded to something that runs at the wrong time.
    """
    from composey.exceptions import ScheduleError
    from composey.models.semantic import RateSchedule

    if not isinstance(schedule, RateSchedule):
        return schedule.expression

    value, unit = schedule.value, schedule.unit

    if unit == "minutes":
        if value == 1:
            return "* * * * *"
        if 60 % value:
            raise ScheduleError(
                f"a rate of every {value} minutes cannot be expressed as cron, "
                f"which Azure requires: use an interval that divides an hour "
                f"evenly, or give a cron expression directly."
            )
        return f"*/{value} * * * *"

    if unit == "hours":
        if value == 1:
            return "0 * * * *"
        if 24 % value:
            raise ScheduleError(
                f"a rate of every {value} hours cannot be expressed as cron, "
                f"which Azure requires: use an interval that divides a day "
                f"evenly, or give a cron expression directly."
            )
        return f"0 */{value} * * *"

    # days
    if value == 1:
        return "0 0 * * *"
    raise ScheduleError(
        f"a rate of every {value} days cannot be expressed as cron, which "
        f"Azure requires: months are not all the same length, so */{value} on "
        f"day-of-month would drift. Give a cron expression directly."
    )


def _infer_scheduled_jobs(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    identity_id: str,
    connections: dict[str, Connection],
) -> None:
    """
    Create a Container Apps Job for each scheduled service.

    A Job runs to completion on its trigger and stops, which is what a schedule
    asks for. Deploying these as Container Apps instead — as composey used to —
    runs a nightly task continuously, and restarts one that exits as soon as it
    has finished.
    """
    for service in app.services:
        if service.capability != "container" or not service.schedule:
            continue

        job_key = service.name
        identity_config = (
            {"type": "UserAssigned", "identity_ids": [identity_id]}
            if identity_id
            else {"type": "SystemAssigned"}
        )

        registry_config = None
        if service.build_context:
            registry = env.container_registry_name or container_registry_name(
                env.name, app.name
            )
            registry_config = [
                {
                    "server": f"{registry}.azurecr.io",
                    "identity": identity_id or "System",
                }
            ]

        resources.azurerm_container_app_job[job_key] = ContainerAppJob(
            name=get_name(service.name),
            resource_group_name=env.name,
            location=env.region,
            container_app_environment_id=(
                "${data.azurerm_container_app_environment.main.id}"
            ),
            schedule_trigger_config=[
                {"cron_expression": _cron_expression(service.schedule)}
            ],
            template=[{"container": [_container_spec(service, app, env, connections)]}],
            identity=identity_config,
            registry=registry_config,
            tags=tags,
        )


def _container_spec(
    service,
    app: SemanticApp,
    env: AzureEnvironment,
    connections: dict[str, Connection],
) -> dict:
    """
    The container block for a service, shared by Container Apps and Jobs.

    Both take the same shape, so a scheduled task gets the same image
    resolution and the same wired-in connection strings as a long-running one.
    cpu and memory sit directly on the container; azurerm has no nested
    "resources" block.
    """
    spec = {
        "name": service.name,
        "image": _get_container_image(service, app, env),
        "cpu": _get_cpu_cores(service),
        "memory": _get_memory_gb(service),
    }

    if service.command:
        spec["args"] = service.command

    env_vars = [{"name": k, "value": v} for k, v in service.env.items()]

    for db_name, conn in connections.items():
        if any(
            r.client == service.name and r.server == db_name for r in app.relationships
        ):
            env_vars.append(
                {
                    "name": f"{db_name.upper()}_URL",
                    "value": f"postgresql://{conn.username}:{conn.password}@{conn.host}:{conn.port}/{conn.database}",
                }
            )

    spec["env"] = env_vars
    return spec


def _infer_container_apps(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    identity_id: str,
    connections: dict[str, Connection],
) -> None:
    """Create Container Apps for each container service."""
    for service in app.services:
        if service.capability != "container":
            continue
        # Scheduled services are Jobs, not always-on apps. See
        # _infer_scheduled_jobs.
        if service.schedule:
            continue

        # Determine scaling configuration
        min_replicas = service.min_scale
        max_replicas = service.max_scale

        # Container Apps can scale to 0 (unlike ECS)
        # But default to at least 1 for web services
        if service.ingress and min_replicas == 0:
            min_replicas = 1

        container_spec = _container_spec(service, app, env, connections)

        # Build ingress config
        ingress_config = None
        if service.ingress:
            ingress_config = {
                "external_enabled": True,
                "target_port": service.ingress.port or service.port or 80,
                "transport": "auto",
                "traffic_weight": {
                    "latest_revision": True,
                    "percentage": 100,
                },
            }

        # Build scale rules. azurerm models HTTP scaling as its own
        # http_scale_rule block with a concurrent_requests string, not as a
        # generic custom rule.
        http_scale_rules = []
        if service.auto_scaling and service.auto_scaling.metrics:
            for metric in service.auto_scaling.metrics:
                if metric.type == "http" or metric.type == "requests_per_target":
                    http_scale_rules.append(
                        {
                            "name": "http-rule",
                            "concurrent_requests": str(int(metric.target_value)),
                        }
                    )

        # If no HTTP rule but has ingress, add default HTTP scaling
        if service.ingress and not http_scale_rules:
            http_scale_rules.append(
                {"name": "http-default", "concurrent_requests": "100"}
            )

        # Build template. Replica counts live directly on the template; there is
        # no "scale" block in the provider schema.
        template = {
            "container": [container_spec],
            "min_replicas": min_replicas,
            "max_replicas": max_replicas,
        }

        if http_scale_rules:
            template["http_scale_rule"] = http_scale_rules

        # Identity config
        identity_config = None
        if identity_id:
            identity_config = {
                "type": "UserAssigned",
                "identity_ids": [identity_id],
            }
        else:
            # Use system-assigned identity
            identity_config = {"type": "SystemAssigned"}

        # Registry config. The server has to be the registry that actually gets
        # created, which is not always the one named on the environment.
        #
        # "System" is the literal the provider expects for a system-assigned
        # identity. Naming the app's own principal_id here instead was a
        # self-reference — the container app cannot depend on itself, and it
        # only pointed at an undeclared azurerm_container_app.main anyway.
        registry_config = None
        if service.build_context:
            registry = env.container_registry_name or container_registry_name(
                env.name, app.name
            )
            registry_config = [
                {
                    "server": f"{registry}.azurecr.io",
                    "identity": identity_id or "System",
                }
            ]

        resources.azurerm_container_app[service.name] = ContainerApp(
            name=get_name(service.name),
            resource_group_name=env.name,
            container_app_environment_id="${data.azurerm_container_app_environment.main.id}",
            template=template,
            ingress=ingress_config,
            identity=identity_config,
            registry=registry_config,
            tags=tags,
        )


def _get_container_image(service, app: SemanticApp, env: AzureEnvironment) -> str:
    """Get the container image reference."""
    if service.build_context:
        # Must resolve to the registry _infer_container_registry creates, so
        # both go through the same naming function.
        registry = env.container_registry_name or container_registry_name(
            env.name, app.name
        )
        return f"{registry}.azurecr.io/{service.name}:latest"
    return service.image


def _get_cpu_cores(service) -> float:
    """Convert service size or explicit CPU to cores."""
    if service.cpu:
        return service.cpu / 1024.0  # Convert from ECS units to cores

    size_map = {
        "small": 0.25,
        "medium": 0.5,
        "large": 1.0,
    }
    return size_map.get(service.size, 0.25)


def _get_memory_gb(service) -> str:
    """Convert service size or explicit memory to GB string."""
    if service.memory:
        return f"{service.memory}Mi"

    size_map = {
        "small": "0.5Gi",
        "medium": "1Gi",
        "large": "2Gi",
    }
    return size_map.get(service.size, "0.5Gi")


def _infer_cdn(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> None:
    """
    Azure CDN is not currently supported.

    These resources model Azure CDN from Microsoft (classic), which no longer
    accepts new profiles:

      Code="BadRequest" Message="Azure CDN from Microsoft (classic) no longer
      support new profile creation."

    So emitting them cannot succeed for anyone. Front Door is the replacement,
    and porting to it is a real piece of work — azurerm_cdn_frontdoor_profile
    plus endpoint, origin group, origin and route — not a rename. Until then
    the request is skipped rather than compiled into an apply that fails after
    everything else has been built.

    The application still deploys; it is served directly from its Container App
    ingress, without a CDN in front.
    """
    cdn_services = [s for s in app.services if s.cdn_enabled and s.ingress]

    if not cdn_services:
        return

    warnings.warn(
        "Azure CDN is not supported: the classic CDN these resources model no "
        "longer accepts new profiles, and Front Door is not implemented yet. "
        "Ignoring cdn for: " + ", ".join(s.name for s in cdn_services),
        stacklevel=2,
    )
    return
