"""Test Azure Blob Storage inference."""

from composey.compiler.inference.azure import infer
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application, Service


def test_storage_account_is_created():
    """An object-storage service creates an Azure Storage Account."""
    env = AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )

    app = Application(
        name="myapp",
        services=[
            Service(
                name="storage",
                image="minio/minio",
                capability="object-storage",
                size="small",
            )
        ],
    )

    resources = infer(app, env)

    assert "storage_storage" in resources.azurerm_storage_account
    account = resources.azurerm_storage_account["storage_storage"]
    assert account.account_tier == "Standard"
    assert account.account_replication_type == "LRS"
    assert account.account_kind == "StorageV2"
    assert account.enable_https_traffic_only is True
    assert account.min_tls_version == "TLS1_2"


def test_storage_container_is_created():
    """A default container is created in the storage account."""
    env = AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )

    app = Application(
        name="myapp",
        services=[
            Service(
                name="assets",
                image="minio/minio",
                capability="object-storage",
                size="medium",
            )
        ],
    )

    resources = infer(app, env)

    assert "assets_container" in resources.azurerm_storage_container
    container = resources.azurerm_storage_container["assets_container"]
    assert container.name == "assets"
    assert container.container_access_type == "private"


def test_storage_connection_is_returned():
    """Storage services return connections for wiring."""
    env = AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )

    app = Application(
        name="myapp",
        services=[
            Service(
                name="uploads",
                image="minio/minio",
                capability="object-storage",
                size="small",
            ),
            Service(
                name="web",
                image="myapp",
                capability="container",
                port=8080,
            ),
        ],
        relationships=[
            {"client": "web", "server": "uploads"},
        ],
    )

    resources = infer(app, env)

    # Storage account and container should be created
    assert "uploads_storage" in resources.azurerm_storage_account
    assert "uploads_container" in resources.azurerm_storage_container
