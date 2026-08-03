"""
A Flexible Server cannot share the Container Apps subnet.

Azure allows a subnet exactly one delegation, and each Flexible Server engine
requires the subnet delegated to itself. Pointing a database at the Container
Apps infrastructure subnet is therefore not a misconfiguration that might work
— it cannot work, and Azure only says so at apply time.
"""

import json

import pytest

from composey.compiler import compile_to_terraform
from composey.compiler.inference.azure import infer
from composey.environment_generator import generate_azure_environment
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application, Service

SUB = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod"
VNET = f"{SUB}/providers/Microsoft.Network/virtualNetworks/prod-vnet"


def _env(**overrides) -> AzureEnvironment:
    kwargs = dict(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id=f"{SUB}/logs",
        vnet_id=VNET,
        infrastructure_subnet_id=f"{VNET}/subnets/infrastructure",
        postgresql_subnet_id=f"{VNET}/subnets/postgresql",
        mysql_subnet_id=f"{VNET}/subnets/mysql",
    )
    kwargs.update(overrides)
    return AzureEnvironment(**kwargs)


def _app(image: str) -> Application:
    return Application(
        name="myapp",
        services=[
            Service(
                name="db",
                image=image,
                capability="database",
                database_name="appdb",
            )
        ],
    )


class TestEnvironmentSubnets:
    def test_creates_a_delegated_subnet_per_engine(self):
        parsed = json.loads(generate_azure_environment("prod", "eastus"))
        subnets = parsed["resource"]["azurerm_subnet"]

        delegations = {
            s["name"]: s["delegation"][0]["service_delegation"][0]["name"]
            for s in subnets.values()
        }
        assert delegations == {
            "infrastructure": "Microsoft.App/environments",
            "postgresql": "Microsoft.DBforPostgreSQL/flexibleServers",
            "mysql": "Microsoft.DBforMySQL/flexibleServers",
        }

    def test_subnets_do_not_overlap(self):
        import ipaddress

        parsed = json.loads(generate_azure_environment("prod", "eastus"))
        nets = [
            ipaddress.ip_network(s["address_prefixes"][0])
            for s in parsed["resource"]["azurerm_subnet"].values()
        ]
        for i, a in enumerate(nets):
            for b in nets[i + 1 :]:
                assert not a.overlaps(b), f"{a} overlaps {b}"

    def test_publishes_the_subnet_ids(self):
        parsed = json.loads(generate_azure_environment("prod", "eastus"))
        published = parsed["output"]["environment"]["value"]
        assert "postgresql_subnet_id" in published
        assert "mysql_subnet_id" in published


class TestServerNetworking:
    @pytest.mark.parametrize(
        "image,engine,resource",
        [
            ("postgres:16", "postgresql", "azurerm_postgresql_flexible_server"),
            ("mysql:8", "mysql", "azurerm_mysql_flexible_server"),
        ],
    )
    def test_server_uses_its_own_subnet_not_the_container_apps_one(
        self, image, engine, resource
    ):
        env = _env()
        resources = infer(_app(image), env)
        server = getattr(resources, resource)["main"]

        assert server.delegated_subnet_id == f"{VNET}/subnets/{engine}"
        assert server.delegated_subnet_id != env.infrastructure_subnet_id

    @pytest.mark.parametrize(
        "image,engine,suffix",
        [("postgres:16", "postgresql", "postgres"), ("mysql:8", "mysql", "mysql")],
    )
    def test_private_dns_zone_is_created_and_linked(self, image, engine, suffix):
        resources = infer(_app(image), _env())

        zone = resources.azurerm_private_dns_zone[engine]
        assert zone.name.endswith(f".{suffix}.database.azure.com")

        link = resources.azurerm_private_dns_zone_virtual_network_link[engine]
        assert link.virtual_network_id == VNET

    def test_server_waits_for_the_dns_link(self):
        """
        Nothing in the server's arguments references the link, so without an
        explicit edge Terraform is free to create the server first — and Azure
        rejects it.
        """
        resources = infer(_app("postgres:16"), _env())
        server = resources.azurerm_postgresql_flexible_server["main"]
        assert server.depends_on == [
            "azurerm_private_dns_zone_virtual_network_link.postgresql"
        ]


class TestOlderEnvironments:
    """
    Environment files written before these subnets existed must stay usable:
    the fields are optional, and a database degrades to public access rather
    than failing to compile.
    """

    def test_falls_back_to_public_access(self):
        env = _env(postgresql_subnet_id=None, mysql_subnet_id=None)
        resources = infer(_app("postgres:16"), env)
        server = resources.azurerm_postgresql_flexible_server["main"]

        assert server.public_network_access_enabled is True
        assert server.delegated_subnet_id is None
        assert not resources.azurerm_private_dns_zone

    def test_environment_without_the_new_fields_still_loads(self):
        AzureEnvironment(
            name="prod",
            region="eastus",
            container_apps_environment_name="prod-env",
            log_analytics_workspace_id=f"{SUB}/logs",
            vnet_id=VNET,
            infrastructure_subnet_id=f"{VNET}/subnets/infrastructure",
        )


def test_compiled_output_wires_the_zone_to_the_server(tmp_path):
    compose = tmp_path / "compose.yml"
    compose.write_text("services:\n  db:\n    image: postgres:16\n")

    parsed = json.loads(compile_to_terraform(str(compose), _env(), "myapp"))
    server = parsed["resource"]["azurerm_postgresql_flexible_server"]["main"]

    assert server["private_dns_zone_id"] == (
        "${azurerm_private_dns_zone.postgresql.id}"
    )
    assert server["public_network_access_enabled"] is False
