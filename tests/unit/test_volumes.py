"""Named volumes are refused; local-only mounts are ignored.

A Docker volume is a POSIX filesystem, and composey has nothing to mount there.
It used to create an S3 bucket instead and mount nothing at all, so writes went
to the task's ephemeral layer, appeared to succeed, and vanished on restart —
separately per replica, so a volume shared between services shared nothing.

Supporting this properly needs a network filesystem, which is a different
product from stateless services with managed backing services. Until then it is
an error rather than a silent one.
"""

import pytest

from composey.compiler.normalizer import normalize
from composey.exceptions import StorageError
from composey.models.compose import Application as DockerApplication
from composey.models.compose import Service as DockerService
from composey.models.compose import VolumeDefinition


def _normalize(volumes, image="web:latest"):
    docker_app = DockerApplication(
        services={"web": DockerService(image=image, volumes=volumes)}
    )
    return normalize(docker_app, "test-project")


def test_a_named_volume_is_refused():
    with pytest.raises(StorageError, match=r"mounts named volume\(s\) db-data"):
        _normalize([VolumeDefinition(type="volume", source="db-data", target="/data")])


def test_the_error_says_what_to_do_instead():
    with pytest.raises(StorageError, match="minio"):
        _normalize(["media:/data/storage"])


def test_every_named_volume_is_listed():
    with pytest.raises(StorageError, match="assets, media"):
        _normalize(["media:/data/media", "assets:/data/assets"])


def test_a_named_volume_on_a_substituted_service_is_accepted():
    # The managed database brings its own storage, which is the point of
    # substituting it, so `db-data:/var/lib/postgresql/data` is simply moot.
    app = _normalize(["db-data:/var/lib/postgresql/data"], image="postgres:16")

    assert app.services[0].capability == "database"


@pytest.mark.parametrize(
    "volumes",
    [
        [VolumeDefinition(type="bind", source="/host/src", target="/code/src")],
        [VolumeDefinition(type="volume", target="/code/node_modules")],
        ["./src:/code/src:ro"],
        ["/etc/hosts:/etc/hosts"],
        ["~/.aws:/aws"],
        ["/code/node_modules"],
    ],
)
def test_local_only_mounts_are_ignored(volumes):
    # Bind mounts inject host paths for development and anonymous volumes are
    # scratch space; neither describes anything that must survive deployment.
    assert _normalize(volumes).services[0].name == "web"


def test_a_bind_mount_alongside_a_named_volume_still_fails():
    with pytest.raises(StorageError, match="media"):
        _normalize(["./src:/code/src", "media:/data"])
