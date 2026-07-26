"""Tests for the decision report.

Weighted towards the warnings: the value of this feature is entirely in making
the silent failures visible, so those are what must not regress.
"""

from composey.compiler.explain import explain, render
from composey.compiler.normalizer import normalize
from composey.models.compose import Application as DockerApplication
from composey.models.compose import Build, Dependency
from composey.models.compose import Port as DockerPort
from composey.models.compose import Service as DockerService


def decisions_for(services: dict):
    docker_app = DockerApplication(services=services)
    return explain(docker_app, normalize(docker_app, "test-project"))


def warnings_for(services: dict) -> list[str]:
    return [d.decision for d in decisions_for(services) if d.source == "warning"]


def test_missing_ingress_is_warned_about():
    # The failure that left two real applications deployed and unreachable.
    warnings = warnings_for(
        {
            "web": DockerService(
                image="web", ports=[DockerPort(target=8080, published=8080)]
            )
        }
    )

    assert "NOT reachable from outside" in warnings


def test_missing_ingress_names_the_candidates():
    decisions = decisions_for(
        {
            "web": DockerService(
                image="web", ports=[DockerPort(target=8080, published=8080)]
            )
        }
    )
    warning = next(d for d in decisions if d.source == "warning")

    assert "web" in warning.because
    assert "x-composey: ingress" in warning.because


def test_declared_ingress_is_not_a_warning():
    decisions = decisions_for(
        {
            "web": DockerService(
                image="web",
                ports=[DockerPort(target=8080, published=8080)],
                **{"x-composey": {"ingress": {}}},
            )
        }
    )

    assert not [d for d in decisions if d.source == "warning"]
    assert any("served at /" in d.decision for d in decisions)


def test_unwired_managed_service_is_warned_about():
    # crucible-ai provisioned RDS and never connected to it, because its
    # POSTGRES_HOST came from a .env saying localhost.
    warnings = warnings_for(
        {
            "web": DockerService(
                image="web",
                environment={"POSTGRES_HOST": "localhost"},
                depends_on={"db": Dependency(condition="service_started")},
            ),
            "db": DockerService(image="postgres:16"),
        }
    )

    assert "nothing wired to db" in warnings


def test_wired_managed_service_is_not_a_warning():
    decisions = decisions_for(
        {
            "web": DockerService(
                image="web",
                environment={"POSTGRES_HOST": "db"},
                depends_on={"db": Dependency(condition="service_started")},
            ),
            "db": DockerService(image="postgres:16"),
        }
    )

    assert any("POSTGRES_HOST → db" in d.decision for d in decisions)
    assert not [d for d in decisions if "nothing wired" in d.decision]


def test_dropped_mounts_are_warned_about():
    warnings = warnings_for(
        {
            "web": DockerService(
                image="web",
                ports=[DockerPort(target=80, published=80)],
                volumes=["./src:/code/src", "/scratch"],
            )
        }
    )

    assert any("mount(s) dropped" in w for w in warnings)


def test_ignored_extra_ports_are_warned_about():
    warnings = warnings_for(
        {
            "web": DockerService(
                image="web",
                ports=[
                    DockerPort(target=80, published=80),
                    DockerPort(target=9229, published=9229),
                ],
            )
        }
    )

    assert any("9229" in w for w in warnings)


def test_empty_secrets_are_warned_about():
    warnings = warnings_for(
        {
            "web": DockerService(
                image="web",
                ports=[DockerPort(target=80, published=80)],
                secrets=["api-key"],
            )
        }
    )

    assert any("api-key" in w and "empty" in w for w in warnings)


def test_substitution_reports_the_image_it_matched():
    decisions = decisions_for({"db": DockerService(image="pgvector/pgvector:pg17")})
    substitution = next(d for d in decisions if "managed database" in d.decision)

    assert "pgvector/pgvector:pg17" in substitution.because
    assert substitution.source == "inferred"


def test_declared_capability_is_reported_as_declared():
    decisions = decisions_for(
        {
            "thing": DockerService(
                image="acme/thing", **{"x-composey": {"capability": "database"}}
            )
        }
    )
    substitution = next(d for d in decisions if "managed database" in d.decision)

    assert substitution.source == "declared"


def test_build_reports_the_dockerfile():
    decisions = decisions_for(
        {
            "web": DockerService(
                build=Build(context=".", dockerfile="./backend/Dockerfile"),
                ports=[DockerPort(target=80, published=80)],
            )
        }
    )

    assert any("./backend/Dockerfile" in d.because for d in decisions)


def test_render_counts_warnings():
    output = render(
        decisions_for(
            {
                "web": DockerService(
                    image="web", ports=[DockerPort(target=8080, published=8080)]
                )
            }
        )
    )

    assert "worth checking" in output
