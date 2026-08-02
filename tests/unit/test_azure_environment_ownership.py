"""
The Container Apps Environment belongs to the platform stack.

`terraform validate` cannot catch a violation here: an app stack that declares
its own Container Apps Environment is perfectly valid on its own, and so is the
environment stack. They only collide at apply time against a real subscription,
with "already exists - to be managed via Terraform this resource needs to be
imported into the State" — twenty minutes into an acceptance run.
"""

import json

from composey.compiler import compile_to_terraform
from composey.compiler.inference.azure import infer
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application, Service


def _env() -> AzureEnvironment:
    return AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )


# Ingress is declared, not inferred from a published port.
_INGRESS_COMPOSE = """\
services:
  web:
    image: nginx
    ports:
      - 80:80
    networks:
      - public
    x-composey:
      ingress: {}
networks:
  public:
"""


def _app() -> Application:
    return Application(
        name="myapp",
        services=[Service(name="web", image="nginx", capability="container")],
    )


def test_app_stack_does_not_manage_the_container_apps_environment():
    resources = infer(_app(), _env())
    assert not resources.azurerm_container_app_environment


def test_container_apps_environment_is_referenced_as_a_data_source(tmp_path):
    compose = tmp_path / "compose.yml"
    compose.write_text("services:\n  web:\n    image: nginx\n    ports:\n      - '80'\n")

    parsed = json.loads(compile_to_terraform(str(compose), _env(), "myapp"))

    assert "azurerm_container_app_environment" not in parsed["resource"]

    lookup = parsed["data"]["azurerm_container_app_environment"]["main"]
    assert lookup["name"] == "prod-env"
    assert lookup["resource_group_name"] == "prod"

    app = parsed["resource"]["azurerm_container_app"]["web"]
    assert (
        app["container_app_environment_id"]
        == "${data.azurerm_container_app_environment.main.id}"
    )


def test_ingress_service_publishes_its_fqdn(tmp_path):
    """
    Unlike AWS, where the hostname belongs to the environment's shared load
    balancer, a Container App carries its own. Without this output nothing
    downstream — the smoke test included — can find the deployed application.
    """
    compose = tmp_path / "compose.yml"
    compose.write_text(_INGRESS_COMPOSE)

    parsed = json.loads(compile_to_terraform(str(compose), _env(), "myapp"))

    assert (
        parsed["output"]["fqdn"]["value"]
        == "${azurerm_container_app.web.ingress[0].fqdn}"
    )


def test_no_fqdn_output_without_ingress(tmp_path):
    compose = tmp_path / "compose.yml"
    compose.write_text("services:\n  worker:\n    image: busybox\n")

    parsed = json.loads(compile_to_terraform(str(compose), _env(), "myapp"))

    assert "output" not in parsed
