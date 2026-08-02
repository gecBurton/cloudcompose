"""Azure resource inference for Composey.

Translates cloud-agnostic semantic model to Azure-specific resources.
Azure Container Apps is a higher-level service than ECS Fargate, so this
module makes opinionated choices that fit Azure's model.
"""

from typing import Callable

from composey.models.aws import RandomPassword
from composey.models.azure import (
    AzureResources,
    CdnEndpoint,
    CdnProfile,
    ContainerApp,
    ContainerAppEnvironment,
    ContainerRegistry,
    KeyVault,
    MySQLFlexibleDatabase,
    MySQLFlexibleServer,
    PostgreSQLFlexibleDatabase,
    PostgreSQLFlexibleServer,
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

    # Step 1: Create Container Apps Environment (if not using existing)
    _infer_container_app_environment(resources, app, env, get_name, tags)

    # Step 2: Create managed identity (or use existing)
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

    # Step 9: Infer CDN for services with cdn_enabled
    _infer_cdn(resources, app, env, get_name, tags)

    return resources


def _infer_container_app_environment(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> None:
    """Create the Container Apps Environment."""
    resources.azurerm_container_app_environment["main"] = ContainerAppEnvironment(
        name=env.container_apps_environment_name,
        resource_group_name=env.name,  # Use environment name as RG
        location=env.region,
        log_analytics_workspace_id=env.log_analytics_workspace_id,
        infrastructure_subnet_id=env.infrastructure_subnet_id,
        tags=tags,
    )


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
        name=get_name("kv").replace("_", "-"),
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

    registry_name = env.container_registry_name or get_name("acr").replace("_", "")

    resources.azurerm_container_registry["main"] = ContainerRegistry(
        name=registry_name,
        resource_group_name=env.name,
        location=env.region,
        sku="Standard",
        admin_enabled=True,  # Required for Container Apps to pull
        tags=tags,
    )


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
            delegated_subnet_id=env.infrastructure_subnet_id,
            public_network_access_enabled=False,
            tags=tags,
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
            delegated_subnet_id=env.infrastructure_subnet_id,
            public_network_access_enabled=False,
            tags=tags,
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
    """Infer Azure Cache for Redis instances.

    Returns a mapping of service name to Connection for use in wiring.
    """
    from composey.constants import DefaultPorts
    from composey.models.azure import RedisCache

    connections: dict[str, Connection] = {}

    cache_services = [s for s in app.services if s.capability == "cache"]

    for service in cache_services:
        cache_key = f"{service.name}_redis"

        # Map size to SKU
        # Basic: Dev/test, no SLA
        # Standard: Production, replication
        # Premium: Production, clustering, VNet
        size_sku_map = {
            "small": ("Standard", "C", 1),  # 1 GB
            "medium": ("Standard", "C", 2),  # 3 GB
            "large": ("Premium", "P", 1),  # 6 GB with clustering
        }
        sku_name, family, capacity = size_sku_map.get(
            service.size, ("Standard", "C", 1)
        )

        # VNet injection for Premium tier
        subnet_id = env.infrastructure_subnet_id if sku_name == "Premium" else None

        resources.azurerm_redis_cache[cache_key] = RedisCache(
            name=get_name(service.name),
            resource_group_name=env.name,
            location=env.region,
            sku_name=sku_name,
            family=family,
            capacity=capacity,
            redis_version="6",
            enable_non_ssl_port=False,
            minimum_tls_version="1.2",
            subnet_id=subnet_id,
            tags=tags,
        )

        # Connection uses primary access key
        connections[service.name] = Connection(
            host=f"${{azurerm_redis_cache.{cache_key}.hostname}}",
            port=DefaultPorts.REDIS,
            password=f"${{azurerm_redis_cache.{cache_key}.primary_access_key}}",
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
        account_name = get_name(service.name).replace("_", "").lower()[:24]

        resources.azurerm_storage_account[account_key] = StorageAccount(
            name=account_name,
            resource_group_name=env.name,
            location=env.region,
            account_tier="Standard",
            account_replication_type="LRS",
            account_kind="StorageV2",
            min_tls_version="TLS1_2",
            enable_https_traffic_only=True,
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

        # Determine scaling configuration
        min_replicas = service.min_scale
        max_replicas = service.max_scale

        # Container Apps can scale to 0 (unlike ECS)
        # But default to at least 1 for web services
        if service.ingress and min_replicas == 0:
            min_replicas = 1

        # Build container spec. cpu and memory sit directly on the container;
        # azurerm has no nested "resources" block.
        container_spec = {
            "name": service.name,
            "image": _get_container_image(service, env),
            "cpu": _get_cpu_cores(service),
            "memory": _get_memory_gb(service),
        }

        if service.command:
            container_spec["args"] = service.command

        # Environment variables
        env_vars = []
        for key, value in service.env.items():
            env_vars.append({"name": key, "value": value})

        # Add connection strings for dependencies
        for db_name, conn in connections.items():
            # Check if this service depends on the database
            if any(
                r.client == service.name and r.server == db_name
                for r in app.relationships
            ):
                env_vars.append(
                    {
                        "name": f"{db_name.upper()}_URL",
                        "value": f"postgresql://{conn.username}:{conn.password}@{conn.host}:{conn.port}/{conn.database}",
                    }
                )

        container_spec["env"] = env_vars

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

        # Registry config
        registry_config = None
        if service.build_context and env.container_registry_name:
            registry_config = [
                {
                    "server": f"{env.container_registry_name}.azurecr.io",
                    "identity": "${azurerm_container_app.main.identity[0].principal_id}"
                    if not identity_id
                    else identity_id,
                }
            ]

        resources.azurerm_container_app[service.name] = ContainerApp(
            name=get_name(service.name),
            resource_group_name=env.name,
            container_app_environment_id="${azurerm_container_app_environment.main.id}",
            template=template,
            ingress=ingress_config,
            identity=identity_config,
            registry=registry_config,
            tags=tags,
        )


def _get_container_image(service, env: AzureEnvironment) -> str:
    """Get the container image reference."""
    if service.build_context:
        # Image from ACR
        registry = env.container_registry_name or f"{env.name}{env.name}acr"
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
    """Infer Azure CDN for services with cdn_enabled."""
    cdn_services = [s for s in app.services if s.cdn_enabled and s.ingress]

    if not cdn_services:
        return

    # Create single CDN profile for the app
    profile_key = "main"
    resources.azurerm_cdn_profile[profile_key] = CdnProfile(
        name=get_name("cdn"),
        resource_group_name=env.name,
        location=env.region,
        sku="Standard_Microsoft",
        tags=tags,
    )

    # Create endpoint for each CDN-enabled service
    for service in cdn_services:
        endpoint_key = f"{service.name}_cdn"

        # Get the Container App FQDN as origin
        origin_host = f"${{azurerm_container_app.{service.name}.ingress[0].fqdn}}"

        resources.azurerm_cdn_endpoint[endpoint_key] = CdnEndpoint(
            name=get_name(f"{service.name}-cdn"),
            profile_name=f"${{azurerm_cdn_profile.{profile_key}.name}}",
            resource_group_name=env.name,
            location=env.region,
            origin_host_header=origin_host,
            origins=[
                {
                    "name": "default",
                    "host_name": origin_host,
                    "http_port": 80,
                    "https_port": 443,
                }
            ],
            is_http_allowed=False,
            is_https_allowed=True,
            optimization_type="GeneralWebDelivery",
            global_delivery_rule={
                "cache_expiration_action": {
                    "behavior": "Override",
                    "duration": "1.00:00:00",  # 1 day cache
                },
            },
            tags=tags,
        )
