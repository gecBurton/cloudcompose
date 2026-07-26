"""Connectivity comes from compose networks, not from depends_on.

`depends_on` describes startup order and constrains nothing under Compose — every
service can reach every other regardless — so rules derived from it were guesses.
`networks:` is the compose file's own statement of who may talk to whom, and it
is enforced locally, so the topology compiled here has already been tested on the
developer's machine.
"""

import json

import pytest

from composey.compiler import compile_to_terraform
from composey.compiler.normalizer import normalize
from composey.models.compose import Application as DockerApplication
from composey.models.compose import NetworkDefinition
from composey.models.compose import Service as DockerService
from composey.models.environment import AwsEnvironment


def _env() -> AwsEnvironment:
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:eu-west-2:123456789012:cluster/c",
        alb_arn="arn:aws:elasticloadbalancing:eu-west-2:1:loadbalancer/app/a/b",
        alb_listener_arn="arn:aws:elasticloadbalancing:eu-west-2:1:listener/app/a/b/c",
    )


def _groups(example: str) -> dict:
    manifest = json.loads(
        compile_to_terraform(f"examples/{example}/compose.yml", _env(), example)
    )
    return manifest["resource"]


def _service_groups(resources: dict, service: str) -> set[str]:
    config = resources["aws_ecs_service"][f"{service}_service"]
    return {
        ref.split(".")[1] for ref in config["network_configuration"]["security_groups"]
    }


def test_disjoint_networks_cannot_reach_each_other():
    # flask puts frontend on `public` and db on `private`. They share no network,
    # so the frontend has no path to the database — which is what the compose
    # file says and what Docker enforces locally.
    resources = _groups("flask")

    frontend = _service_groups(resources, "frontend")
    database = set(resources["aws_db_instance"]["db_db"]["vpc_security_group_ids"])
    database = {ref.split(".")[1] for ref in database}

    assert not frontend & database


def test_a_service_on_both_networks_reaches_both():
    resources = _groups("flask")

    backend = _service_groups(resources, "backend")

    assert {"public_sg", "private_sg"} <= backend


def test_members_of_a_network_can_reach_each_other():
    rule = _groups("flask")["aws_security_group_rule"]["private_sg_internal_rule"]

    assert rule["type"] == "ingress"
    assert rule["source_security_group_id"] == "${aws_security_group.private_sg.id}"
    assert rule["protocol"] == "-1"


def test_a_file_with_no_networks_is_flat():
    # Compose materialises a single `default` network, so everything can reach
    # everything — the behaviour of a compose file that says nothing.
    resources = _groups("scaling")

    assert "default_sg" in resources["aws_security_group"]
    assert _service_groups(resources, "web") >= {"default_sg"}


def test_load_balancer_ingress_is_scoped_to_one_service():
    # The rule lives on a group attached to the public service alone. On a
    # network group it would open the port for every other service there.
    resources = _groups("flask")

    assert "backend_ingress_sg" in resources["aws_security_group"]
    assert "backend_ingress_sg" in _service_groups(resources, "backend")
    assert "backend_ingress_sg" not in _service_groups(resources, "frontend")


def test_relationships_no_longer_create_rules():
    # depends_on used to produce `web_to_db_rule` and friends.
    rules = _groups("flask")["aws_security_group_rule"]

    assert not [name for name in rules if "_to_" in name and "alb_" not in name]


def _normalize(networks_per_service: dict, top_level: dict | None = None):
    docker_app = DockerApplication(
        services={
            name: DockerService(image=name, networks={n: None for n in networks})
            for name, networks in networks_per_service.items()
        },
        networks=top_level or {},
    )
    return normalize(docker_app, "app")


def test_networks_are_sorted_for_determinism():
    app = _normalize({"web": ["zebra", "alpha"]})

    assert app.services[0].networks == ["alpha", "zebra"]


def test_too_many_networks_is_rejected():
    with pytest.raises(ValueError, match="joins 6 networks"):
        _normalize({"web": [f"net{i}" for i in range(6)]})


def test_external_networks_are_rejected():
    with pytest.raises(ValueError, match="declared external"):
        _normalize(
            {"web": ["shared"]},
            {"shared": NetworkDefinition(external=True)},
        )
