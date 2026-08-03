"""Docker Compose to Semantic Model Normalizer.

Transforms parsed Docker Compose models into cloud-agnostic semantic models.
This is Stage 2 of the compiler pipeline.
"""

import re
from typing import Optional, Union

from pydantic import ValidationError

from composey.constants import (
    BIND_SOURCE_PREFIXES,
    CAPABILITY_IMAGES,
    CRON_WRAPPER_PATTERN,
    DATABASE_NAME_ALLOWED_CHARS,
    DATABASE_NAME_MAX_LENGTH,
    DEFAULT_NETWORK_NAME,
    MAX_NETWORKS_PER_SERVICE,
    RATE_PATTERN,
    database_name_variables,
)
from composey.exceptions import (
    NetworkError,
    StorageError,
)
from composey.exceptions import (
    ValidationError as ComposeyValidationError,
)

from ..models.compose import Application as DockerApplication
from ..models.compose import Service as DockerService
from ..models.compose import VolumeDefinition, XComposey
from ..models.semantic import (
    Application as SemanticApplication,
)
from ..models.semantic import (
    AutoScalingConfig as SemanticAutoScalingConfig,
)
from ..models.semantic import (
    AutoScalingMetric as SemanticAutoScalingMetric,
)
from ..models.semantic import (
    CronSchedule,
    RateSchedule,
    Relationship,
    Schedule,
)
from ..models.semantic import (
    Service as SemanticService,
)


def _parse_schedule(raw: str) -> Schedule:
    """Parse a schedule into the cloud-neutral model.

    The canonical spellings are a standard 5-field cron expression
    ("0 2 * * *") or an interval ("every 1 hour"). AWS's own `cron(...)` and
    `rate(...)` forms are also accepted, since they predate this model, but the
    provider dialect is not what gets stored — each backend renders its own.
    """
    text = raw.strip()

    rate = re.match(RATE_PATTERN, text, re.IGNORECASE)
    if rate:
        value = int(rate.group(1)) if rate.group(1) else 1
        return RateSchedule(value=value, unit=f"{rate.group(2).lower()}s")

    wrapped = re.match(CRON_WRAPPER_PATTERN, text, re.IGNORECASE)
    fields = (wrapped.group(1) if wrapped else text).split()

    # EventBridge cron carries a sixth year field that standard cron lacks.
    if len(fields) == 6:
        fields = fields[:5]
    if len(fields) != 5:
        raise ComposeyValidationError(
            f"schedule {raw!r} is not a 5-field cron expression or an interval "
            f'like "every 1 hour"'
        )

    # '?' is an AWS placeholder meaning "no value here"; standard cron uses '*'.
    fields = ["*" if f == "?" else f for f in fields]
    return CronSchedule(expression=" ".join(fields))


def _infer_capability(image: str) -> str:
    """Guess what an image really is from its name.

    Inference can never be complete — an image can be called anything — so this
    is only a default. `x-composey: capability:` overrides it, which is the
    supported way to correct a wrong guess or to classify a private image.
    """
    reference = image.lower().split("@")[0]
    # Drop the tag, taking care not to mistake a registry port for one.
    path, _, last = reference.rpartition("/")
    segments = f"{path}/{last.split(':')[0]}".strip("/").split("/")

    # A leading registry host is addressing, not identity
    if len(segments) > 1 and ("." in segments[0] or segments[0] == "localhost"):
        segments = segments[1:]

    # A segment must be one of the known names exactly
    for capability, known in CAPABILITY_IMAGES.items():
        if any(segment in known for segment in segments):
            return capability

    return "container"


def _database_name(
    app_name: str, service_name: str, environment: dict[str, Optional[str]]
) -> str:
    """The database to create inside a managed instance.

    A name the compose file states is used as written, because the application
    was tested against it. Anything else is composey's own choice, and it is
    made compound -- application and service -- rather than the bare service
    name, so that the compiler cannot pick a name the engine refuses.

    RDS rejects a DBName that is a reserved word for the engine, and `db` is one
    on Postgres. Since `db` is about the most likely name for a database service
    in a compose file, the bare service name was the one default guaranteed to
    hit this.
    """
    for variable in database_name_variables:
        stated = environment.get(variable)
        if stated:
            return _sanitize_database_name(stated)

    compound = f"{app_name}_{service_name}" if app_name else service_name
    return _sanitize_database_name(compound)


def _sanitize_database_name(raw: str) -> str:
    """Coerce a name into one every supported engine accepts.

    Postgres and MySQL both allow only letters, digits and underscores, and
    Postgres requires the first character to be a letter.
    """
    cleaned = "".join(c if c.isalnum() else "_" for c in raw.lower()).lstrip(
        DATABASE_NAME_ALLOWED_CHARS
    )
    return (cleaned or "app")[:DATABASE_NAME_MAX_LENGTH]


def _reject_persistent_volumes(
    name: str, service: DockerService, capability: str
) -> None:
    """Refuse a named volume on a service composey runs as a container.

    A Docker volume is a POSIX filesystem, and composey has nothing to mount
    there: ECS gives the task an ephemeral layer, so writes appear to succeed
    and vanish on the next restart, separately per replica.
    """
    if capability != "container":
        return

    named = sorted(
        {
            source
            for volume in service.volumes or []
            if (source := _named_volume_source(volume)) is not None
        }
    )
    if not named:
        return

    raise StorageError(
        f"service {name!r} mounts named volume(s) {', '.join(named)}",
        details=(
            "Composey cannot provide a persistent filesystem, and running the service "
            "without one would lose whatever is written there on every restart. "
            "Use a `minio` service for object storage, or drop the volume if the "
            "path only needs scratch space, which the task already has."
        ),
    )


def _named_volume_source(volume: Union[str, VolumeDefinition]) -> Optional[str]:
    """Return the named volume a mount refers to, or None if it isn't one.

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
    if source.startswith(BIND_SOURCE_PREFIXES):
        return None
    return source


def _network_segments_for(
    name: str, service: DockerService, reserved: int = 0
) -> list[str]:
    """The network isolation segments a service belongs to.

    Docker networks become cloud-agnostic isolation segments. Services sharing a
    segment can reach each other; services in disjoint segments cannot.
    These map to:
    - AWS: Security groups
    - GCP: VPC connector memberships
    - Azure: VNet integration / private endpoints
    """
    segments = sorted(service.networks or {}) or [DEFAULT_NETWORK_NAME]

    # A publicly reachable service also gets a dedicated group for the load
    # balancer, which counts against the same quota.
    if len(segments) + reserved > MAX_NETWORKS_PER_SERVICE:
        raise NetworkError(
            f"service {name!r} joins {len(segments)} network segments ({', '.join(segments)})",
            details=(
                f"At most {MAX_NETWORKS_PER_SERVICE - reserved} are supported here, "
                f"because each becomes a security group (AWS) or equivalent "
                f"isolation mechanism and clouds have limits on attachments."
            ),
        )

    return segments


def _reject_unsupported_networks(app: DockerApplication) -> None:
    """External networks name infrastructure composey does not own."""
    external = sorted(
        name
        for name, definition in app.networks.items()
        if definition is not None and definition.external
    )
    if external:
        raise NetworkError(
            f"networks {', '.join(external)} are declared external",
            details="Composey cannot map a network it does not create to a security group",
        )


def _settings_for(name: str, service: DockerService) -> XComposey:
    """Validate a service's x-composey block, naming the service if it is wrong."""
    try:
        return XComposey.model_validate(service.x_composey_raw)
    except ValidationError as error:
        raise ComposeyValidationError(
            f"service {name!r} has an invalid x-composey block",
            details=str(error),
        ) from error


def normalize(app: DockerApplication, project_name: str) -> SemanticApplication:
    """Transform a Docker Compose application into a semantic model.

    This is the main entry point for the normalization stage.
    """
    semantic_services = []
    relationships = []

    _reject_unsupported_networks(app)

    for s_name, docker_service in app.services.items():
        settings = _settings_for(s_name, docker_service)
        ingress = settings.exposure
        network_segments = _network_segments_for(
            s_name, docker_service, reserved=1 if ingress else 0
        )

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

        capability = _infer_capability(docker_service.image or "")

        # Normalize command to ECS exec form (a list)
        command = None
        if isinstance(docker_service.command, list):
            command = docker_service.command
        elif isinstance(docker_service.command, str):
            command = ["/bin/sh", "-c", docker_service.command]

        # Build context for building from source
        build_context = docker_service.build.context if docker_service.build else None
        dockerfile = docker_service.build.dockerfile if docker_service.build else None

        # An explicit capability always beats what the image name suggests
        if settings.capability is not None:
            capability = settings.capability

        schedule = _parse_schedule(settings.schedule) if settings.schedule else None

        # Parse auto-scaling configuration
        auto_scaling = None
        if settings.auto_scaling:
            auto_scaling = SemanticAutoScalingConfig(
                metrics=[
                    SemanticAutoScalingMetric(
                        type=m.type,
                        target_value=m.target,
                    )
                    for m in settings.auto_scaling.metrics
                ],
                scale_in_cooldown=settings.auto_scaling.scale_in_cooldown,
                scale_out_cooldown=settings.auto_scaling.scale_out_cooldown,
            )

        _reject_persistent_volumes(s_name, docker_service, capability)

        semantic_services.append(
            SemanticService(
                name=s_name,
                image=docker_service.image or "placeholder",
                capability=capability,
                size=settings.size,
                cpu=settings.cpu,
                memory=settings.memory,
                port=primary_port,
                database_name=(
                    _database_name(project_name, s_name, docker_service.environment)
                    if capability == "database"
                    else None
                ),
                build_context=build_context,
                dockerfile=dockerfile,
                command=command,
                startup_grace_period=settings.grace_period,
                min_scale=settings.min_scale,
                max_scale=settings.max_scale,
                auto_scaling=auto_scaling,
                schedule=schedule,
                cdn_enabled=settings.cdn,
                ingress=ingress,
                network_isolation_segments=network_segments,
                env=docker_service.environment,
                config=docker_service.platform_env,
                secrets=secret_names,
            )
        )

        # Build relationships
        for dep_name in docker_service.depends_on.keys():
            relationships.append(Relationship(client=s_name, server=dep_name))

    # Fill in default ingress port from service port
    for service in semantic_services:
        if service.ingress and service.ingress.port is None:
            service.ingress.port = service.port or 80

    _reject_overlapping_paths(semantic_services)

    return SemanticApplication(
        name=project_name,
        services=semantic_services,
        relationships=relationships,
    )


def _reject_overlapping_paths(services: list[SemanticService]) -> None:
    """Two services cannot serve the same path."""
    from composey.exceptions import IngressError

    seen: dict[str, str] = {}
    for service in services:
        if service.ingress is None:
            continue
        path = service.ingress.path
        if path in seen:
            raise IngressError(
                f"services {seen[path]!r} and {service.name!r} both serve {path!r}",
                details="Give each ingress a distinct path",
            )
        seen[path] = service.name
