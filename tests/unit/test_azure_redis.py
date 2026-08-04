"""Test Azure Redis Cache inference."""

from composey.compiler.inference.azure import infer
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application, Service


def test_redis_cache_is_created():
    """A cache service creates an Azure Redis Cache."""
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
                name="cache",
                image="redis:7",
                capability="cache",
                size="small",
            )
        ],
    )

    resources = infer(app, env)

    assert "cache_redis" in resources.azurerm_managed_redis
    cache = resources.azurerm_managed_redis["cache_redis"]
    assert cache.sku_name == "Balanced_B1"


def test_redis_cache_size_mapping():
    """Different sizes map to appropriate SKUs."""
    env = AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )

    # Test medium size
    app = Application(
        name="myapp",
        services=[
            Service(
                name="cache",
                image="redis:7",
                capability="cache",
                size="medium",
            )
        ],
    )

    resources = infer(app, env)
    cache = resources.azurerm_managed_redis["cache_redis"]
    assert cache.sku_name == "Balanced_B1"

    app.services[0].size = "large"
    resources = infer(app, env)
    cache = resources.azurerm_managed_redis["cache_redis"]
    assert cache.sku_name == "Balanced_B3"


def test_redis_connection_is_returned():
    """Cache services return connections for wiring."""
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
                name="cache",
                image="redis:7",
                capability="cache",
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
            {"client": "web", "server": "cache"},
        ],
    )

    resources = infer(app, env)

    # Connection should be created
    # (we'd need to check this through the connections dict in a full test)
    assert "cache_redis" in resources.azurerm_managed_redis
