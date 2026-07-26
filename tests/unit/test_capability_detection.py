"""Classifying what an image really is, and correcting it when we guess wrong.

The cases here are drawn from real compose files rather than invented: a
vendored `pgvector/pgvector` ran as a container with its data on ephemeral
storage, and an application called `flask-redis-web` was very nearly classified
as a cache.
"""

import pytest

from composey.compiler.normalizer import _infer_capability, normalize
from composey.models.compose import Application as DockerApplication
from composey.models.compose import Port as DockerPort
from composey.models.compose import Service as DockerService


@pytest.mark.parametrize(
    "image",
    [
        "postgres:16",
        "postgres",
        "pgvector/pgvector:pg17",
        "postgis/postgis:16-3.4",
        "timescale/timescaledb:latest-pg16",
        "bitnami/postgresql:16",
        "public.ecr.aws/docker/library/postgres:16",
        "mysql:8",
        "mariadb:10-focal",
        "percona:8.0",
    ],
)
def test_database_images(image):
    assert _infer_capability(image) == "database"


@pytest.mark.parametrize(
    "image", ["redis:7", "redislabs/redismod", "valkey/valkey:8", "keydb:latest"]
)
def test_cache_images(image):
    assert _infer_capability(image) == "cache"


def test_object_storage_images():
    assert _infer_capability("minio/minio") == "object-storage"


@pytest.mark.parametrize(
    "image",
    [
        "",
        "nginx:latest",
        "nginxdemos/hello:plain-text",
        # Application images that merely mention a service they talk to. A
        # containment match classified these as managed services.
        "flask-redis-web:latest",
        "my-postgres-admin:1.0",
        "acme/redis-dashboard",
        # A registry host is addressing, not identity.
        "redis.example.com/acme/webapp:1",
    ],
)
def test_application_images_are_containers(image):
    assert _infer_capability(image) == "container"


def _normalize(**x_composey):
    docker_app = DockerApplication(
        services={
            "thing": DockerService(
                image="acme/private-thing:1", **{"x-composey": x_composey}
            )
        }
    )
    return normalize(docker_app, "test-project").services[0]


def test_capability_can_be_declared_explicitly():
    # The escape hatch for a private image whose name says nothing useful.
    assert _normalize(capability="database").capability == "database"


def test_capability_override_beats_inference():
    docker_app = DockerApplication(
        services={
            "db": DockerService(
                image="postgres:16", **{"x-composey": {"capability": "container"}}
            )
        }
    )

    assert normalize(docker_app, "p").services[0].capability == "container"


def test_unknown_capability_is_rejected_by_name():
    with pytest.raises(ValueError, match="unknown capability 'databse'"):
        _normalize(capability="databse")


def _public_app(**overrides):
    return DockerApplication(
        services={
            "frontend": DockerService(
                image="frontend",
                ports=[DockerPort(target=8081, published=8081)],
                **{"x-composey": overrides.get("frontend", {})},
            ),
            "backend": DockerService(
                image="backend",
                ports=[DockerPort(target=8080, published=8080)],
                **{"x-composey": overrides.get("backend", {})},
            ),
        }
    )


def test_no_public_service_is_detected_from_non_standard_ports():
    # The behaviour that left two real applications deployed but unreachable.
    assert normalize(_public_app(), "p").public_service is None


def test_public_can_be_declared_explicitly():
    app = _public_app(frontend={"public": True})

    assert normalize(app, "p").public_service == "frontend"


def test_declaring_two_public_services_is_rejected():
    app = _public_app(frontend={"public": True}, backend={"public": True})

    with pytest.raises(ValueError, match="more than one service"):
        normalize(app, "p")
