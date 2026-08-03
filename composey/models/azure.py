"""Azure resource models for Terraform generation.

These models represent Azure resources that the compiler can generate.
Each model maps to an AzureRM Terraform resource.
"""

from typing import Any, Dict, List, Optional

from pydantic import BaseModel, ConfigDict, Field


class ContainerApp(BaseModel):
    """Azure Container App resource."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    container_app_environment_id: str

    # Container configuration
    revision_mode: str = Field(default="Single", description="Single or Multiple")

    # Template (container spec)
    template: Dict[str, Any] = Field(
        default_factory=dict,
        description="Container template with containers, scale rules, etc.",
    )

    # Ingress configuration
    ingress: Optional[Dict[str, Any]] = Field(
        default=None,
        description="Ingress configuration (external or internal)",
    )

    # Identity
    identity: Optional[Dict[str, Any]] = Field(
        default=None,
        description="Managed identity configuration",
    )

    # Registry
    registry: Optional[List[Dict[str, str]]] = Field(
        default=None,
        description="Container registry configuration",
    )

    tags: Optional[Dict[str, str]] = None


class ContainerAppEnvironment(BaseModel):
    """Azure Container Apps Environment."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    location: str

    log_analytics_workspace_id: str

    # VNet integration
    infrastructure_subnet_id: Optional[str] = None
    internal_load_balancer_enabled: bool = False

    tags: Optional[Dict[str, str]] = None


class ContainerRegistry(BaseModel):
    """Azure Container Registry (ACR)."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    location: str
    sku: str = Field(default="Standard", description="Basic, Standard, or Premium")
    admin_enabled: bool = False

    tags: Optional[Dict[str, str]] = None


class PostgreSQLFlexibleServer(BaseModel):
    """Azure Database for PostgreSQL - Flexible Server."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    location: str

    # Version and SKU
    version: str = Field(default="14", description="PostgreSQL version")
    sku_name: str = Field(default="B_Standard_B1ms", description="SKU name")
    storage_mb: int = Field(default=32768, description="Storage in MB (32GB)")

    # Authentication
    administrator_login: str
    administrator_password: str

    # Networking
    delegated_subnet_id: Optional[str] = None
    private_dns_zone_id: Optional[str] = None
    public_network_access_enabled: bool = False

    # High availability
    high_availability: Optional[Dict[str, str]] = None

    # Database to create
    database_name: Optional[str] = None

    # The private DNS zone must be linked to the VNet before the server is
    # created, and no argument here references the link, so the edge has to be
    # declared explicitly.
    depends_on: Optional[List[str]] = None

    tags: Optional[Dict[str, str]] = None


class PostgreSQLFlexibleDatabase(BaseModel):
    """Database within a PostgreSQL Flexible Server."""

    model_config = ConfigDict(extra="forbid")

    name: str
    server_id: str
    charset: str = "UTF8"
    collation: str = "en_US.utf8"


class MySQLFlexibleServer(BaseModel):
    """Azure Database for MySQL - Flexible Server."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    location: str

    # Version and SKU
    version: str = Field(default="8.0", description="MySQL version")
    sku_name: str = Field(default="B_Standard_B1ms", description="SKU name")
    storage_mb: int = Field(default=32768, description="Storage in MB (32GB)")

    # Authentication
    administrator_login: str
    administrator_password: str

    # Networking
    delegated_subnet_id: Optional[str] = None
    private_dns_zone_id: Optional[str] = None
    public_network_access_enabled: bool = False

    # High availability
    high_availability: Optional[Dict[str, str]] = None

    # See PostgreSQLFlexibleServer.depends_on.
    depends_on: Optional[List[str]] = None

    tags: Optional[Dict[str, str]] = None


class MySQLFlexibleDatabase(BaseModel):
    """Database within a MySQL Flexible Server."""

    model_config = ConfigDict(extra="forbid")

    name: str
    server_id: str
    charset: str = "utf8mb4"
    collation: str = "utf8mb4_unicode_ci"


class PrivateDnsZone(BaseModel):
    """
    Private DNS zone for a Flexible Server.

    A server on a delegated subnet is unreachable by name without one, and
    Azure refuses to create it: EmptyPrivateDnsZoneArmResourceId. The zone name
    must end in the engine's suffix (.postgres.database.azure.com).
    """

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    tags: Optional[Dict[str, str]] = None


class PrivateDnsZoneVirtualNetworkLink(BaseModel):
    """Attaches a private DNS zone to the environment's VNet."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    private_dns_zone_name: str
    virtual_network_id: str
    registration_enabled: bool = False
    tags: Optional[Dict[str, str]] = None


class KeyVault(BaseModel):
    """Azure Key Vault for secrets."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    location: str
    tenant_id: str
    sku_name: str = "standard"

    soft_delete_retention_days: int = 7
    purge_protection_enabled: bool = False

    tags: Optional[Dict[str, str]] = None


class KeyVaultSecret(BaseModel):
    """Secret stored in Azure Key Vault."""

    model_config = ConfigDict(extra="forbid")

    name: str
    key_vault_id: str
    value: str

    # Don't show the value in Terraform output
    lifecycle: Optional[Dict[str, List[str]]] = Field(
        default_factory=lambda: {"ignore_changes": ["value"]}
    )


class UserAssignedIdentity(BaseModel):
    """Azure User-Assigned Managed Identity."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    location: str
    tags: Optional[Dict[str, str]] = None


class RoleAssignment(BaseModel):
    """Azure RBAC Role Assignment."""

    model_config = ConfigDict(extra="forbid")

    scope: str
    role_definition_name: str
    principal_id: str


class StorageAccount(BaseModel):
    """Azure Storage Account for Blob Storage."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    location: str

    account_tier: str = Field(
        default="Standard",
        description="Standard or Premium",
    )
    account_replication_type: str = Field(
        default="LRS",
        description="LRS, GRS, RAGRS, ZRS, GZRS, or RAGZRS",
    )
    account_kind: str = Field(
        default="StorageV2",
        description="StorageV2, Storage, or BlobStorage",
    )

    # Security
    min_tls_version: str = "TLS1_2"
    enable_https_traffic_only: bool = True

    tags: Optional[Dict[str, str]] = None


class StorageContainer(BaseModel):
    """Container within an Azure Storage Account."""

    model_config = ConfigDict(extra="forbid")

    name: str
    storage_account_name: str
    container_access_type: str = Field(
        default="private",
        description="private, blob, or container",
    )


class CdnProfile(BaseModel):
    """Azure CDN Profile."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    location: str
    sku: str = Field(
        default="Standard_Microsoft",
        description="Standard_Microsoft, Standard_Verizon, Standard_Akamai, or Premium_Verizon",
    )

    tags: Optional[Dict[str, str]] = None


class CdnEndpoint(BaseModel):
    """Azure CDN Endpoint."""

    model_config = ConfigDict(extra="forbid")

    name: str
    profile_name: str
    resource_group_name: str
    location: str

    # Origin configuration
    origin_host_header: str
    # Named "origin": the provider's block is singular, and Terraform JSON
    # spells repeated blocks as a list under the singular name.
    origin: List[Dict[str, Any]]

    # HTTPS
    is_http_allowed: bool = False
    is_https_allowed: bool = True

    # Optimizations
    optimization_type: str = Field(
        default="GeneralWebDelivery",
        description="GeneralWebDelivery, DynamicSiteAcceleration, etc.",
    )

    # Cache rules
    global_delivery_rule: Optional[Dict[str, Any]] = None
    delivery_rule: Optional[List[Dict[str, Any]]] = None

    tags: Optional[Dict[str, str]] = None


class RedisCache(BaseModel):
    """Azure Cache for Redis."""

    model_config = ConfigDict(extra="forbid")

    name: str
    resource_group_name: str
    location: str

    # SKU configuration
    sku_name: str = Field(
        default="Standard",
        description="Basic, Standard, or Premium",
    )
    family: str = Field(
        default="C",
        description="C for Basic/Standard, P for Premium",
    )
    capacity: int = Field(
        default=1,
        description="0-6 for C family (Basic/Standard), 1-5 for P family (Premium)",
    )

    # Redis configuration
    redis_version: str = Field(default="6", description="Redis version 4 or 6")
    enable_non_ssl_port: bool = False
    minimum_tls_version: str = "1.2"

    # Networking (Premium tier only)
    subnet_id: Optional[str] = Field(
        default=None,
        description="Subnet ID for VNet injection (Premium tier only)",
    )

    # Persistence (Premium tier only)
    redis_configuration: Optional[Dict[str, Any]] = Field(
        default=None,
        description="Redis configuration including persistence settings",
    )

    tags: Optional[Dict[str, str]] = None


class AzureResources(BaseModel):
    """A registry of the Azure resources our compiler supports."""

    azurerm_container_app: Dict[str, ContainerApp] = Field(default_factory=dict)
    azurerm_container_app_environment: Dict[str, ContainerAppEnvironment] = Field(
        default_factory=dict
    )
    azurerm_container_registry: Dict[str, ContainerRegistry] = Field(
        default_factory=dict
    )
    azurerm_postgresql_flexible_server: Dict[str, PostgreSQLFlexibleServer] = Field(
        default_factory=dict
    )
    azurerm_postgresql_flexible_server_database: Dict[
        str, PostgreSQLFlexibleDatabase
    ] = Field(default_factory=dict)
    azurerm_mysql_flexible_server: Dict[str, MySQLFlexibleServer] = Field(
        default_factory=dict
    )
    azurerm_mysql_flexible_database: Dict[str, MySQLFlexibleDatabase] = Field(
        default_factory=dict
    )
    azurerm_private_dns_zone: Dict[str, PrivateDnsZone] = Field(default_factory=dict)
    azurerm_private_dns_zone_virtual_network_link: Dict[
        str, PrivateDnsZoneVirtualNetworkLink
    ] = Field(default_factory=dict)
    azurerm_key_vault: Dict[str, KeyVault] = Field(default_factory=dict)
    azurerm_key_vault_secret: Dict[str, KeyVaultSecret] = Field(default_factory=dict)
    azurerm_user_assigned_identity: Dict[str, UserAssignedIdentity] = Field(
        default_factory=dict
    )
    azurerm_role_assignment: Dict[str, RoleAssignment] = Field(default_factory=dict)
    azurerm_redis_cache: Dict[str, RedisCache] = Field(default_factory=dict)
    azurerm_storage_account: Dict[str, StorageAccount] = Field(default_factory=dict)
    azurerm_storage_container: Dict[str, StorageContainer] = Field(default_factory=dict)
    azurerm_cdn_profile: Dict[str, CdnProfile] = Field(default_factory=dict)
    azurerm_cdn_endpoint: Dict[str, CdnEndpoint] = Field(default_factory=dict)

    # Docker provider resources (same as AWS)
    docker_image: Dict[str, Any] = Field(default_factory=dict)
    docker_registry_image: Dict[str, Any] = Field(default_factory=dict)

    # Random resources for passwords
    random_password: Dict[str, Any] = Field(default_factory=dict)
