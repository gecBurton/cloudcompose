from composey.compiler.normalizer import normalize
from composey.models.compose import (
    Application as DockerApplication,
)
from composey.models.compose import (
    Service as DockerService,
)
from composey.models.compose import (
    VolumeDefinition,
)


def _storage_for(volumes) -> list[str]:
    docker_app = DockerApplication(
        services={"web": DockerService(image="web:latest", volumes=volumes)}
    )
    return normalize(docker_app, "test-project").services[0].storage


def test_named_volume_becomes_storage():
    assert _storage_for(
        [VolumeDefinition(type="volume", source="db-data", target="/var/lib/mysql")]
    ) == ["db-data"]


def test_bind_mount_is_not_storage():
    # A bind mount injects a host path for local development; it has no
    # meaning in a deployed environment and must not become a bucket.
    assert (
        _storage_for(
            [
                VolumeDefinition(
                    type="bind", source="/home/dev/app/src", target="/code/src"
                )
            ]
        )
        == []
    )


def test_anonymous_volume_is_not_storage():
    assert (
        _storage_for([VolumeDefinition(type="volume", target="/code/node_modules")])
        == []
    )


def test_short_form_named_volume_becomes_storage():
    assert _storage_for(["db-data:/var/lib/mysql"]) == ["db-data"]


def test_short_form_bind_mounts_are_not_storage():
    assert (
        _storage_for(["./src:/code/src:ro", "/etc/hosts:/etc/hosts", "~/.aws:/aws"])
        == []
    )


def test_short_form_anonymous_volume_is_not_storage():
    assert _storage_for(["/code/node_modules"]) == []


def test_mixed_mounts_keep_only_named_volumes():
    assert _storage_for(
        [
            VolumeDefinition(type="bind", source="/host/src", target="/code/src"),
            VolumeDefinition(type="volume", source="uploads", target="/data"),
            VolumeDefinition(type="tmpfs", target="/tmp"),
        ]
    ) == ["uploads"]
