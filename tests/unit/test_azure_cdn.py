"""Test Azure CDN inference."""

from composey.compiler.inference.azure import infer
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application, Ingress, Service


def test_cdn_profile_is_created_for_cdn_enabled_service():
    """A service with cdn_enabled creates a CDN profile and endpoint."""
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
                name="web",
                image="myapp/web:latest",
                capability="container",
                port=8080,
                ingress=Ingress(path="/"),
                cdn_enabled=True,
            )
        ],
    )

    resources = infer(app, env)

    # CDN profile should be created
    assert "main" in resources.azurerm_cdn_profile
    profile = resources.azurerm_cdn_profile["main"]
    assert profile.sku == "Standard_Microsoft"

    # CDN endpoint should be created
    assert "web_cdn" in resources.azurerm_cdn_endpoint
    endpoint = resources.azurerm_cdn_endpoint["web_cdn"]
    assert endpoint.is_https_allowed is True
    assert endpoint.is_http_allowed is False
    assert endpoint.optimization_type == "GeneralWebDelivery"


def test_no_cdn_created_without_cdn_enabled():
    """Services without cdn_enabled don't create CDN resources."""
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
                name="web",
                image="myapp/web:latest",
                capability="container",
                port=8080,
                ingress=Ingress(path="/"),
                cdn_enabled=False,
            )
        ],
    )

    resources = infer(app, env)

    # No CDN resources should be created
    assert not resources.azurerm_cdn_profile
    assert not resources.azurerm_cdn_endpoint


def test_single_cdn_profile_for_multiple_services():
    """Multiple CDN-enabled services share a single CDN profile."""
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
                name="web",
                image="myapp/web:latest",
                capability="container",
                port=8080,
                ingress=Ingress(path="/"),
                cdn_enabled=True,
            ),
            Service(
                name="api",
                image="myapp/api:latest",
                capability="container",
                port=8080,
                ingress=Ingress(path="/api"),
                cdn_enabled=True,
            ),
        ],
    )

    resources = infer(app, env)

    # Single profile
    assert len(resources.azurerm_cdn_profile) == 1
    assert "main" in resources.azurerm_cdn_profile

    # Two endpoints
    assert len(resources.azurerm_cdn_endpoint) == 2
    assert "web_cdn" in resources.azurerm_cdn_endpoint
    assert "api_cdn" in resources.azurerm_cdn_endpoint
