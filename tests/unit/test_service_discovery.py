"""Services finding each other by name.

Compose puts every service on a shared network where siblings resolve by name.
An ECS task has no name at all, so a compose file whose frontend calls
`http://backend:8000` compiled to an environment variable pointing at nothing —
silently, for eight days, because no example had two containers talking.
"""

import json

import pytest
from composey.compiler.normalizer import normalize
from composey.compiler.parser import parse

from composey.compiler import compile_to_terraform
from composey.compiler.explain import explain
from composey.compiler.inference import _namespace_for, infer
from composey.models.environment import AwsEnvironment
from composey.models.semantic import (
    Application,
    CronSchedule,
    Relationship,
    Service,
)


def _env() -> AwsEnvironment:
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:eu-west-2:1:cluster/c",
    )


def _example() -> dict:
    return json.loads(
        compile_to_terraform("examples/web-api/compose.yml", _env(), "web-api")
    )["resource"]


def test_a_reference_to_a_sibling_resolves_to_its_dns_name():
    container = json.loads(
        _example()["aws_ecs_task_definition"]["web_td"]["container_definitions"]
    )[0]
    env = {e["name"]: e["value"] for e in container["environment"]}

    assert env["API_URL"] == "http://api.prod-web-api.internal:80"


def test_reachable_services_are_registered():
    resources = _example()

    assert "app" in resources["aws_service_discovery_private_dns_namespace"]
    assert set(resources["aws_service_discovery_service"]) == {
        "api_discovery",
        "web_discovery",
    }


def test_the_ecs_service_registers_itself():
    service = _example()["aws_ecs_service"]["api_service"]

    assert service["service_registries"] == {
        "registry_arn": "${aws_service_discovery_service.api_discovery.arn}"
    }


def test_records_return_every_replica():
    # One address per task under awsvpc; MULTIVALUE spreads clients across them
    # instead of pinning every caller to whichever came back first.
    config = _example()["aws_service_discovery_service"]["api_discovery"]["dns_config"]

    assert config["routing_policy"] == "MULTIVALUE"
    assert config["dns_records"] == [{"ttl": 10, "type": "A"}]


@pytest.mark.parametrize(
    "environment,application,expected",
    [
        ("prod", "web-api", "prod-web-api.internal"),
        ("prod", "my_app", "prod-my-app.internal"),
        ("Prod", "App", "prod-app.internal"),
    ],
)
def test_the_namespace_is_scoped_and_dns_safe(environment, application, expected):
    # Cloud Map namespaces are unique per VPC and applications routinely share
    # one, so the name has to carry both.
    assert _namespace_for(environment, application) == expected


def _infer(services, relationships=None):
    app = Application(name="app", services=services, relationships=relationships or [])
    return infer(app, _env())


def test_a_service_with_no_port_is_not_registered():
    resources = _infer([Service(name="worker", image="worker")])

    assert not resources.aws_service_discovery_service
    assert not resources.aws_service_discovery_private_dns_namespace


def test_a_scheduled_task_is_not_registered():
    # It runs and exits rather than being something to find.
    resources = _infer(
        [
            Service(
                name="job",
                image="job",
                port=80,
                schedule=CronSchedule(expression="0 2 * * *"),
            )
        ]
    )

    assert not resources.aws_service_discovery_service


def test_referencing_an_unreachable_sibling_is_reported():
    docker_app = parse("examples/web-api/compose.yml")
    semantic = normalize(docker_app, "web-api")
    # Strip the port that makes `api` findable.
    next(s for s in semantic.services if s.name == "api").port = None

    warnings = [d for d in explain(docker_app, semantic) if d.source == "warning"]

    assert any("cannot reach api" in d.decision for d in warnings)


def test_managed_services_are_unaffected():
    resources = _infer(
        [
            Service(name="web", image="web", port=80),
            Service(
                name="db",
                image="postgres:16",
                capability="database",
                database_name="app_db",
            ),
        ],
        [Relationship(client="web", server="db")],
    )

    assert set(resources.aws_service_discovery_service) == {"web_discovery"}
