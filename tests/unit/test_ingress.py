"""Ingress: reachable, on which port, at what path, healthy by what measure.

These were one boolean derived from whether a published port happened to be 80
or 443. That single rule produced four separate faults, one per question it was
conflating, and each of them is pinned here.
"""

import json

import pytest

from composey.compiler.generator import generate
from composey.compiler.inference import _path_patterns, _priority_band, infer
from composey.compiler.normalizer import normalize
from composey.models.compose import Application as DockerApplication
from composey.models.compose import Port as DockerPort
from composey.models.compose import Service as DockerService
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application, Ingress, Service

ALB = "arn:aws:elasticloadbalancing:eu-west-2:123456789012:loadbalancer/app/shared/abc"
LISTENER = "arn:aws:elasticloadbalancing:eu-west-2:123456789012:listener/app/shared/a/b"


def _env() -> AwsEnvironment:
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:eu-west-2:123456789012:cluster/c",
        alb_arn=ALB,
        alb_listener_arn=LISTENER,
    )


def _compile(app: Application) -> dict:
    env = _env()
    return json.loads(generate(infer(app, env), env))


def _service(name: str, **ingress) -> Service:
    return Service(
        name=name,
        image=name,
        port=ingress.pop("service_port", 8080),
        ingress=Ingress(**ingress),
    )


def test_a_service_on_a_non_standard_port_is_reachable():
    # The fault that left two real applications deployed and unreachable.
    manifest = _compile(Application(name="app", services=[_service("web")]))

    group = manifest["resource"]["aws_lb_target_group"]["web_tg"]
    assert group["port"] == 8080
    assert manifest["resource"]["aws_ecs_service"]["web_service"]["load_balancer"]


def test_health_check_path_is_honoured():
    # Both real projects serve /api/healthcheck; a hardcoded / would have failed
    # every health check and cycled the tasks forever.
    manifest = _compile(
        Application(name="app", services=[_service("web", health_path="/api/health")])
    )

    group = manifest["resource"]["aws_lb_target_group"]["web_tg"]
    assert group["health_check"]["path"] == "/api/health"


def test_several_services_can_be_public():
    app = Application(
        name="app",
        services=[_service("frontend", path="/"), _service("backend", path="/api")],
    )

    manifest = _compile(app)

    assert set(manifest["resource"]["aws_lb_target_group"]) == {
        "frontend_tg",
        "backend_tg",
    }


def test_more_specific_paths_are_evaluated_first():
    app = Application(
        name="app",
        services=[_service("frontend", path="/"), _service("backend", path="/api")],
    )

    rules = _compile(app)["resource"]["aws_lb_listener_rule"]

    assert (
        rules["backend_listener_rule"]["priority"]
        < (rules["frontend_listener_rule"]["priority"])
    )


def test_two_applications_do_not_collide_on_one_listener():
    # priority was hardcoded to 100, so a second application deploying to the
    # same shared listener failed to apply.
    first = _compile(Application(name="alpha", services=[_service("web")]))
    second = _compile(Application(name="beta", services=[_service("web")]))

    assert (
        first["resource"]["aws_lb_listener_rule"]["web_listener_rule"]["priority"]
        != second["resource"]["aws_lb_listener_rule"]["web_listener_rule"]["priority"]
    )


def test_priority_is_stable_across_runs():
    # Determinism is a stated guarantee, so the band cannot come from a salted
    # hash. Python's builtin hash() of a string would break this.
    assert _priority_band("alpha") == _priority_band("alpha")
    assert 1 <= _priority_band("alpha") <= 50000


def test_priority_can_be_declared():
    app = Application(name="app", services=[_service("web", priority=42)])

    rules = _compile(app)["resource"]["aws_lb_listener_rule"]
    assert rules["web_listener_rule"]["priority"] == 42


@pytest.mark.parametrize(
    "path,patterns",
    [
        ("/", ["/*"]),
        ("/api", ["/api", "/api/*"]),
        ("/api/", ["/api", "/api/*"]),
        ("/a/b", ["/a/b", "/a/b/*"]),
    ],
)
def test_path_patterns(path, patterns):
    assert _path_patterns(path) == patterns


def test_internal_services_get_no_ingress():
    manifest = _compile(
        Application(name="app", services=[Service(name="worker", image="worker")])
    )

    assert "aws_lb_target_group" not in manifest["resource"]


def _normalized(**per_service):
    docker_app = DockerApplication(
        services={
            name: DockerService(
                image=name,
                ports=[DockerPort(target=8080, published=port)],
                **{"x-composey": settings},
            )
            for name, (port, settings) in per_service.items()
        }
    )
    return normalize(docker_app, "app")


def test_publishing_80_does_not_imply_exposure():
    # There is no port convention. "Publishes 80 so it is public, publishes 8080
    # so it is unreachable" was not something a reader could work out, and it
    # silently decided the most consequential property a service has.
    app = _normalized(web=(80, {}))

    assert app.public_services == []


def test_exposure_is_only_ever_declared():
    app = _normalized(legacy=(80, {}), api=(8080, {"ingress": {"path": "/api"}}))

    assert [s.name for s in app.public_services] == ["api"]


def test_public_shorthand_declares_a_default_route():
    app = _normalized(web=(80, {"ingress": {}}))

    assert [s.name for s in app.public_services] == ["web"]
    assert app.services[0].ingress.path == "/"


def test_ingress_port_defaults_to_the_service_port():
    app = _normalized(web=(8080, {"ingress": {}}))

    assert app.services[0].ingress.port == 8080


def test_ingress_port_can_be_declared():
    app = _normalized(web=(8080, {"ingress": {"port": 9000}}))

    assert app.services[0].ingress.port == 9000


def test_bare_ingress_key_declares_a_default_route():
    # `ingress:` with nothing under it parses as null. Treating that as "not
    # exposed" would put back the silent non-exposure this design removes.
    app = _normalized(web=(80, {"ingress": None}))

    assert [s.name for s in app.public_services] == ["web"]
    assert app.services[0].ingress.path == "/"


def test_the_public_shorthand_is_gone():
    # One way to declare a route, not two.
    with pytest.raises(ValueError, match="public"):
        _normalized(web=(80, {"public": True}))
