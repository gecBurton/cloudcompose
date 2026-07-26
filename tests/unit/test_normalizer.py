from composey.compiler.normalizer import normalize
from composey.models.compose import (
    Application as DockerApplication,
)
from composey.models.compose import (
    Dependency,
)
from composey.models.compose import (
    Port as DockerPort,
)
from composey.models.compose import (
    Service as DockerService,
)


def test_normalize_basic_service():
    # Setup a mock DockerApplication
    docker_app = DockerApplication(
        services={
            "web": DockerService(
                image="nginx:latest",
                ports=[DockerPort(target=80, published=80, protocol="tcp")],
                environment={"DEBUG": "true"},
            )
        }
    )

    semantic_app = normalize(docker_app, "test-project")

    assert semantic_app.name == "test-project"
    assert len(semantic_app.services) == 1
    assert semantic_app.services[0].name == "web"
    assert semantic_app.services[0].port == 80
    assert [s.name for s in semantic_app.public_services] == ["web"]
    assert semantic_app.services[0].env["DEBUG"] == "true"


def test_normalize_relationships():
    docker_app = DockerApplication(
        services={
            "web": DockerService(
                image="web:latest",
                ports=[DockerPort(target=80, published=80)],
                depends_on={"db": Dependency(condition="service_started")},
            ),
            "db": DockerService(image="postgres:latest", ports=[]),
        }
    )

    semantic_app = normalize(docker_app, "test-project")

    assert len(semantic_app.relationships) == 1
    rel = semantic_app.relationships[0]
    assert rel.client == "web"
    assert rel.server == "db"
    assert [s.name for s in semantic_app.public_services] == ["web"]


def test_normalize_no_public_service():
    docker_app = DockerApplication(
        services={"worker": DockerService(image="worker:latest", ports=[])}
    )

    semantic_app = normalize(docker_app, "test-project")

    assert semantic_app.public_services == []
    assert semantic_app.services[0].port is None


def test_normalize_multiple_ports_takes_first():
    docker_app = DockerApplication(
        services={
            "web": DockerService(
                image="web:latest",
                ports=[
                    DockerPort(target=3000, published=80),
                    DockerPort(target=9000, published=9000),
                ],
            )
        }
    )

    semantic_app = normalize(docker_app, "test-project")

    # It should pick the target of the first port entry
    assert semantic_app.services[0].port == 3000
    assert [s.name for s in semantic_app.public_services] == ["web"]


def test_normalize_scaling():
    docker_app = DockerApplication(
        services={
            "web": DockerService(
                image="web:latest",
                **{"x-composey": {"min_scale": 2, "max_scale": 10}},
            )
        }
    )

    semantic_app = normalize(docker_app, "test-project")

    assert semantic_app.services[0].min_scale == 2
    assert semantic_app.services[0].max_scale == 10
