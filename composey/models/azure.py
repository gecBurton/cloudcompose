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

    tags: Optional[Dict[str, str]] = None


class PostgreSQLFlexibleDatabase(BaseModel):
    """Database within a PostgreSQL Flexible Server."""

    model_config = ConfigDict(extra="forbid")

    name: str
    server_id: str
    charset: str = "UTF8"
    collation: str = "en_US.utf8"


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
    azurerm_key_vault: Dict[str, KeyVault] = Field(default_factory=dict)
    azurerm_key_vault_secret: Dict[str, KeyVaultSecret] = Field(default_factory=dict)
    azurerm_user_assigned_identity: Dict[str, UserAssignedIdentity] = Field(
        default_factory=dict
    )
    azurerm_role_assignment: Dict[str, RoleAssignment] = Field(default_factory=dict)

    # Docker provider resources (same as AWS)
    docker_image: Dict[str, Any] = Field(default_factory=dict)
    docker_registry_image: Dict[str, Any] = Field(default_factory=dict)

    # Random resources for passwords
    random_password: Dict[str, Any] = Field(default_factory=dict)
