"""
Test Azure CDN inference.

Azure CDN is currently unsupported: see TestCdnIsUnsupported. The tests that
asserted a profile and endpoint were created have been removed rather than
updated — they described output the Azure API rejects outright.
"""

from composey.compiler.inference.azure import infer
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application, Ingress, Service


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


class TestCdnIsUnsupported:
    """
    The classic CDN these resources modelled no longer accepts new profiles
    ("Azure CDN from Microsoft (classic) no longer support new profile
    creation"), so compiling them produced an apply that failed after
    everything else had been built. Front Door is the replacement and is not
    implemented yet.
    """

    def _app_with_cdn(self):
        from composey.models.semantic import Application, Ingress, Service

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

    def _env(self):
        from composey.models.environment import AzureEnvironment

        return AzureEnvironment(
            name="prod",
            region="uksouth",
            container_apps_environment_name="prod-env",
            log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
            vnet_id="/subscriptions/123/vnets/prod",
            infrastructure_subnet_id="/subscriptions/123/subnets/prod",
        )

    def test_no_cdn_resources_are_emitted(self):
        from composey.compiler.inference.azure import infer

        resources = infer(self._app_with_cdn(), self._env())

        assert not resources.azurerm_cdn_profile
        assert not resources.azurerm_cdn_endpoint

    def test_the_request_is_not_dropped_silently(self):
        import warnings as _w

        from composey.compiler.inference.azure import infer

        with _w.catch_warnings(record=True) as caught:
            _w.simplefilter("always")
            infer(self._app_with_cdn(), self._env())

        assert any("CDN is not supported" in str(c.message) for c in caught)

    def test_the_application_still_deploys(self):
        from composey.compiler.inference.azure import infer

        resources = infer(self._app_with_cdn(), self._env())
        assert "web" in resources.azurerm_container_app
