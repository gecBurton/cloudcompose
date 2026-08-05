"""
Test Azure Front Door inference for `cdn: true`.

Front Door replaced Azure CDN from Microsoft (classic), which this module
used to model: that product no longer accepts new profiles at all ("Azure
CDN from Microsoft (classic) no longer support new profile creation"). See
TODO.md for the billing check that unblocked implementing this (the $35/month
Front Door Standard base fee is billed hourly and only for hours used).
"""

from composey.compiler.inference.azure import infer
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application, Ingress, Service


def _env(region: str = "francecentral") -> AzureEnvironment:
    return AzureEnvironment(
        name="prod",
        region=region,
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )


def test_no_cdn_created_without_cdn_enabled():
    """Services without cdn_enabled don't create any Front Door resources."""
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

    resources = infer(app, _env())

    assert not resources.azurerm_cdn_frontdoor_profile
    assert not resources.azurerm_cdn_frontdoor_endpoint
    assert not resources.azurerm_cdn_frontdoor_origin_group
    assert not resources.azurerm_cdn_frontdoor_origin
    assert not resources.azurerm_cdn_frontdoor_route


def test_cdn_enabled_without_ingress_creates_nothing():
    """cdn_enabled with no ingress has no Container App FQDN to front."""
    app = Application(
        name="myapp",
        services=[
            Service(
                name="worker",
                image="myapp/worker:latest",
                capability="container",
                cdn_enabled=True,
            )
        ],
    )

    resources = infer(app, _env())

    assert not resources.azurerm_cdn_frontdoor_profile


class TestFrontDoor:
    """`cdn: true` on a service with an ingress puts Front Door in front of it."""

    def _app_with_cdn(self):
        return Application(
            name="myapp",
            services=[
                Service(
                    name="web",
                    image="nginx",
                    capability="container",
                    ingress=Ingress(port=80),
                    cdn_enabled=True,
                )
            ],
        )

    def test_creates_one_profile_per_application(self):
        resources = infer(self._app_with_cdn(), _env())

        assert list(resources.azurerm_cdn_frontdoor_profile.keys()) == ["main"]
        profile = resources.azurerm_cdn_frontdoor_profile["main"]
        assert profile.sku_name == "Standard_AzureFrontDoor"
        # Front Door is global: no location field exists on this model at all.
        assert not hasattr(profile, "location")

    def test_creates_endpoint_origin_group_origin_and_route_per_service(self):
        resources = infer(self._app_with_cdn(), _env())

        assert "web" in resources.azurerm_cdn_frontdoor_endpoint
        assert "web" in resources.azurerm_cdn_frontdoor_origin_group
        assert "web" in resources.azurerm_cdn_frontdoor_origin
        assert "web" in resources.azurerm_cdn_frontdoor_route

    def test_origin_points_at_the_container_app_ingress_fqdn(self):
        resources = infer(self._app_with_cdn(), _env())

        origin = resources.azurerm_cdn_frontdoor_origin["web"]
        fqdn = "${azurerm_container_app.web.ingress[0].fqdn}"
        assert origin.host_name == fqdn
        # Container Apps requires the Host header to match what it is
        # listening for, so this must be set explicitly rather than left to
        # default to Front Door's own hostname.
        assert origin.origin_host_header == fqdn

    def test_route_references_the_origin_for_terraform_ordering(self):
        resources = infer(self._app_with_cdn(), _env())

        route = resources.azurerm_cdn_frontdoor_route["web"]
        origin_id = "${azurerm_cdn_frontdoor_origin.web.id}"
        assert route.cdn_frontdoor_origin_ids == [origin_id]

    def test_the_application_still_deploys(self):
        resources = infer(self._app_with_cdn(), _env())
        assert "web" in resources.azurerm_container_app

    def test_multiple_cdn_services_share_one_profile(self):
        app = Application(
            name="myapp",
            services=[
                Service(
                    name="web",
                    image="nginx",
                    capability="container",
                    ingress=Ingress(port=80),
                    cdn_enabled=True,
                ),
                Service(
                    name="api",
                    image="nginx",
                    capability="container",
                    ingress=Ingress(port=8080),
                    cdn_enabled=True,
                ),
            ],
        )

        resources = infer(app, _env())

        assert len(resources.azurerm_cdn_frontdoor_profile) == 1
        assert set(resources.azurerm_cdn_frontdoor_origin.keys()) == {"web", "api"}
