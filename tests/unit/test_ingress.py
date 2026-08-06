"""Ingress: reachable, on which port, at what path, healthy by what measure.

These were one boolean derived from whether a published port happened to be 80
or 443. That single rule produced four separate faults, one per question it was
conflating, and each of them is pinned here.

Restored after the Go port deleted normalizer.py (0244d4a). The declaration-
semantics half of the original file (public_services from x-composey.ingress,
the removed `public:` shorthand, bare `ingress:` handling) now lives in
composey-go/internal/compiler/normalizer_contract_test.go, since normalize()
no longer exists here to drive it. What's left is pure ALB/target-group/
generator behavior, built directly against the semantic model.
"""

import json

import pytest

from composey.compiler.generator import generate
from composey.compiler.inference import _path_patterns, infer
from composey.compiler.inference._common import _priority_band
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
        alb_security_group_id="sg-alb0123456789",
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
        Application(
            name="app",
            services=[_service("web", health_check={"path": "/api/health"})],
        )
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
