"""
Azure's naming rules differ per resource type and are enforced at apply time,
so a name that is merely plausible costs a failed deployment to discover.
"""

import re

import pytest

from composey.compiler.inference.azure.naming import (
    container_registry_name,
    key_vault_name,
    storage_account_name,
)

LONG = "nginx-flask-mysql-with-a-very-long-application-name"


class TestContainerRegistryName:
    @pytest.mark.parametrize("app", ["flask", LONG, "a"])
    def test_obeys_azure_rules(self, app):
        name = container_registry_name("prod", app)
        assert re.fullmatch(r"[a-zA-Z0-9]+", name), name
        assert 5 <= len(name) <= 50

    def test_keeps_short_names_readable(self):
        assert container_registry_name("prod", "flask") == "prodflaskacr"


class TestStorageAccountName:
    @pytest.mark.parametrize("app", ["flask-s3", LONG, "a"])
    def test_obeys_azure_rules(self, app):
        name = storage_account_name("prod", app, "blobs")
        assert re.fullmatch(r"[a-z0-9]+", name), name
        assert 3 <= len(name) <= 24

    def test_keeps_short_names_readable(self):
        assert storage_account_name("prod", "flask", "blobs") == "prodflaskblobs"


class TestKeyVaultName:
    @pytest.mark.parametrize("app", ["hello", "nginx-flask-mysql", LONG, "a"])
    def test_obeys_azure_rules(self, app):
        name = key_vault_name("prod", app)
        assert re.fullmatch(r"[a-zA-Z][a-zA-Z0-9-]*[a-zA-Z0-9]", name), name
        assert "--" not in name
        assert 3 <= len(name) <= 24

    def test_keeps_short_names_readable(self):
        assert key_vault_name("prod", "hello") == "prod-hello-kv"

    def test_starts_with_a_letter_even_when_the_environment_does_not(self):
        assert key_vault_name("123", "app")[0].isalpha()


class TestUniqueness:
    """
    Truncation is where names collide, and a collision means the second
    deployment fails against the first deployment's resource.
    """

    def test_applications_sharing_a_truncated_prefix_stay_distinct(self):
        a = key_vault_name("prod", "nginx-flask-mysql-service")
        b = key_vault_name("prod", "nginx-flask-mysql-serviceX")
        assert a != b

    def test_storage_accounts_sharing_a_truncated_prefix_stay_distinct(self):
        a = storage_account_name("prod", "a-very-long-application-name", "blobs")
        b = storage_account_name("prod", "a-very-long-application-names", "blobs")
        assert a != b

    def test_names_are_stable_across_calls(self):
        assert key_vault_name("prod", LONG) == key_vault_name("prod", LONG)


def test_built_images_point_at_the_registry_that_is_created():
    """
    The registry name was computed in two places that disagreed: the registry
    was created as "prod-flask-acr" while images referenced
    "prodprodacr.azurecr.io" — the environment name twice, no application name.
    Both now resolve through the same function.
    """
    import json

    from composey.compiler import compile_to_terraform
    from composey.models.environment import AzureEnvironment

    env = AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )

    parsed = json.loads(
        compile_to_terraform("examples/flask/compose.yml", env, "flask")
    )

    registry = parsed["resource"]["azurerm_container_registry"]["main"]["name"]
    apps = parsed["resource"]["azurerm_container_app"].values()
    images = [app["template"]["container"][0]["image"] for app in apps]

    assert images, "expected at least one built image"
    for image in images:
        assert image.startswith(f"{registry}.azurecr.io/"), image
