from typing import Optional, Union

from ..models.compose import Application as DockerApplication
from ..models.compose import VolumeDefinition
from ..models.semantic import (
    Application as SemanticApplication,
)
from ..models.semantic import (
    Relationship,
)
from ..models.semantic import (
    Service as SemanticService,
)

# Prefixes that mark a short-form volume source as a host path rather than a
# named volume, matching how Compose itself disambiguates the two.
_BIND_SOURCE_PREFIXES = ("/", "./", "../", "~")


def _named_volume_source(volume: Union[str, VolumeDefinition]) -> Optional[str]:
    """
    Return the named volume a mount refers to, or None if it isn't one.

    Only named volumes describe storage that outlives the container. Bind
    mounts inject host paths for local development and anonymous volumes are
    scratch space, so neither has a meaning in a deployed environment.
    """
    if isinstance(volume, VolumeDefinition):
        return volume.source if volume.type == "volume" else None

    # Short form: "source:target[:mode]". A single field is an anonymous volume.
    parts = volume.split(":")
    if len(parts) < 2:
        return None

    source = parts[0]
    if source.startswith(_BIND_SOURCE_PREFIXES):
        return None
    return source


def normalize(app: DockerApplication, project_name: str) -> SemanticApplication:
    semantic_services = []
    relationships = []
    public_service = None

    for s_name, docker_service in app.services.items():
        # Identify the public service (first one mapping to port 80 or 443)
        if docker_service.ports:
            for p in docker_service.ports:
                if p.published in [80, 443] and public_service is None:
                    public_service = s_name

        # Handle services without ports safely
        primary_port = docker_service.ports[0].target if docker_service.ports else None

        # Resolve secrets to names
        secret_names = []
        if docker_service.secrets:
            for s in docker_service.secrets:
                if isinstance(s, str):
                    secret_names.append(s)
                else:
                    secret_names.append(s.source)

        # Resolve named volumes to storage names
        storage_names = []
        for v in docker_service.volumes or []:
            source = _named_volume_source(v)
            if source:
                storage_names.append(source)

        # Infer capability from image name
        capability = "container"
        image_lower = (docker_service.image or "").lower()

        # Database detection: starts with or matches specific library images
        db_images = ["postgres", "mysql", "mariadb"]
        if any(
            image_lower.startswith(db) or f"/{db}" in image_lower for db in db_images
        ):
            capability = "database"
        # Cache detection
        elif any(
            image_lower.startswith(c) or f"/{c}" in image_lower
            for c in ["redis", "valkey"]
        ):
            capability = "cache"
        # Storage detection
        elif any(
            image_lower.startswith(s) or f"/{s}" in image_lower for s in ["minio"]
        ):
            capability = "object-storage"

        # Normalize command to ECS exec form (a list). A string is shell form,
        # so wrap it the way Docker would: /bin/sh -c "<string>".
        command = None
        if isinstance(docker_service.command, list):
            command = docker_service.command
        elif isinstance(docker_service.command, str):
            command = ["/bin/sh", "-c", docker_service.command]

        # A service with a build context is built and pushed to ECR rather than
        # pulled. Kept relative to the compose file for deterministic output.
        build_context = docker_service.build.context if docker_service.build else None

        # Extract x-composey size/resource hints
        size = "small"
        cpu = None
        memory = None
        min_scale = 1
        max_scale = 1
        schedule = None
        cdn_enabled = False
        health_check_grace_period = None

        x_composey = docker_service.x_composey
        if "size" in x_composey:
            size = x_composey["size"]
        if "cpu" in x_composey:
            cpu = int(x_composey["cpu"])
        if "memory" in x_composey:
            memory = int(x_composey["memory"])
        if "min_scale" in x_composey:
            min_scale = int(x_composey["min_scale"])
        if "max_scale" in x_composey:
            max_scale = int(x_composey["max_scale"])
        if "schedule" in x_composey:
            schedule = x_composey["schedule"]
        if "cdn" in x_composey:
            cdn_enabled = bool(x_composey["cdn"])
        if "health_check_grace_period" in x_composey:
            health_check_grace_period = int(x_composey["health_check_grace_period"])

        semantic_services.append(
            SemanticService(
                name=s_name,
                image=docker_service.image or "placeholder",
                capability=capability,
                size=size,
                cpu=cpu,
                memory=memory,
                port=primary_port,
                build_context=build_context,
                command=command,
                health_check_grace_period=health_check_grace_period,
                min_scale=min_scale,
                max_scale=max_scale,
                schedule=schedule,
                cdn_enabled=cdn_enabled,
                env=docker_service.environment,
                secrets=secret_names,
                storage=storage_names,
            )
        )

        # Build relationships
        for dep_name in docker_service.depends_on.keys():
            relationships.append(Relationship(client=s_name, server=dep_name))

    return SemanticApplication(
        name=project_name,
        services=semantic_services,
        relationships=relationships,
        public_service=public_service,
    )
