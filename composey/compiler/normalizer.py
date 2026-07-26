import re
from typing import Optional, Union

from pydantic import ValidationError

from ..models.compose import Application as DockerApplication
from ..models.compose import Service as DockerService
from ..models.compose import VolumeDefinition, XComposey
from ..models.semantic import (
    Application as SemanticApplication,
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

# "every 30 minutes", "every hour", and the AWS "rate(1 hour)" spelling.
_RATE = re.compile(
    r"^(?:rate\(\s*|every\s+)(?:(\d+)\s+)?(minute|hour|day)s?\s*\)?$", re.IGNORECASE
)
_CRON_WRAPPER = re.compile(r"^cron\(\s*(.*?)\s*\)$", re.IGNORECASE)


def _parse_schedule(raw: str) -> Schedule:
    """
    Parse a schedule into the cloud-neutral model.

    The canonical spellings are a standard 5-field cron expression
    ("0 2 * * *") or an interval ("every 1 hour"). AWS's own `cron(...)` and
    `rate(...)` forms are also accepted, since they predate this model, but the
    provider dialect is not what gets stored — each backend renders its own.
    """
    text = raw.strip()

    rate = _RATE.match(text)
    if rate:
        value = int(rate.group(1)) if rate.group(1) else 1
        return RateSchedule(value=value, unit=f"{rate.group(2).lower()}s")

    wrapped = _CRON_WRAPPER.match(text)
    fields = (wrapped.group(1) if wrapped else text).split()

    # EventBridge cron carries a sixth year field that standard cron lacks.
    if len(fields) == 6:
        fields = fields[:5]
    if len(fields) != 5:
        raise ValueError(
            f"schedule {raw!r} is not a 5-field cron expression or an interval "
            f'like "every 1 hour"'
        )

    # '?' is an AWS placeholder meaning "no value here"; standard cron uses '*'.
    fields = ["*" if f == "?" else f for f in fields]
    return CronSchedule(expression=" ".join(fields))


# Substrings that identify what a library image really is. Matched against each
# path segment of the image reference, so vendored and mirrored images resolve
# too: pgvector/pgvector, bitnami/postgresql and public.ecr.aws/.../postgres all
# name a database. Matching only the three canonical names missed every one of
# those, and the failure was silent — a database ran as a container with its
# data directory on ephemeral storage.
_CAPABILITY_IMAGES: dict[str, frozenset[str]] = {
    "database": frozenset(
        {
            "postgres",
            "postgresql",
            "pgvector",
            "postgis",
            "timescaledb",
            "mysql",
            "mariadb",
            "percona",
            "percona-server-mysql",
        }
    ),
    "cache": frozenset({"redis", "redismod", "redis-stack", "valkey", "keydb"}),
    "object-storage": frozenset({"minio"}),
}


def _infer_capability(image: str) -> str:
    """
    Guess what an image really is from its name.

    Inference can never be complete — an image can be called anything — so this
    is only a default. `x-composey: capability:` overrides it, which is the
    supported way to correct a wrong guess or to classify a private image.
    """
    reference = image.lower().split("@")[0]
    # Drop the tag, taking care not to mistake a registry port for one.
    path, _, last = reference.rpartition("/")
    segments = f"{path}/{last.split(':')[0]}".strip("/").split("/")

    # A leading registry host is addressing, not identity: ghcr.io or
    # redis.example.com say nothing about what the image contains.
    if len(segments) > 1 and ("." in segments[0] or segments[0] == "localhost"):
        segments = segments[1:]

    # A segment must be one of the known names exactly. Looser matching cannot
    # tell redislabs/redismod (a cache) from acme/redis-dashboard (an app that
    # merely talks to one), and guessing wrong deploys a managed service as a
    # container — or worse, an application as a database.
    for capability, known in _CAPABILITY_IMAGES.items():
        if any(segment in known for segment in segments):
            return capability

    return "container"


# Prefixes that mark a short-form volume source as a host path rather than a
# named volume, matching how Compose itself disambiguates the two.
_BIND_SOURCE_PREFIXES = ("/", "./", "../", "~")


def _reject_persistent_volumes(
    name: str, service: DockerService, capability: str
) -> None:
    """
    Refuse a named volume on a service composey runs as a container.

    A Docker volume is a POSIX filesystem, and composey has nothing to mount
    there: ECS gives the task an ephemeral layer, so writes appear to succeed
    and vanish on the next restart, separately per replica. Supporting this
    properly means a network filesystem, which is a different product from
    stateless services with managed backing services.

    Volumes on a substituted service are fine and ignored — the managed database
    or cache brings its own storage, which is the point of substituting it.
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

    raise ValueError(
        f"service {name!r} mounts named volume(s) {', '.join(named)}; composey "
        f"cannot provide a persistent filesystem, and running the service "
        f"without one would lose whatever is written there on every restart. "
        f"Use a `minio` service for object storage, or drop the volume if the "
        f"path only needs scratch space, which the task already has."
    )


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


# One security group per network, and AWS attaches at most five to a task's
# network interface before the quota has to be raised.
MAX_NETWORKS_PER_SERVICE = 5

DEFAULT_NETWORK = "default"


def _networks_for(name: str, service: DockerService, reserved: int = 0) -> list[str]:
    """
    The networks a service is attached to.

    Compose already answers who may talk to whom: services sharing a network can
    reach each other, and services on disjoint networks cannot. Unlike
    depends_on, which describes startup order and constrains nothing, this is
    enforced locally — so a compose file that works on a laptop has already
    tested the topology being compiled here.
    """
    networks = sorted(service.networks or {}) or [DEFAULT_NETWORK]

    # A publicly reachable service also gets a dedicated group for the load
    # balancer, which counts against the same quota.
    if len(networks) + reserved > MAX_NETWORKS_PER_SERVICE:
        raise ValueError(
            f"service {name!r} joins {len(networks)} networks "
            f"({', '.join(networks)}); at most "
            f"{MAX_NETWORKS_PER_SERVICE - reserved} are supported here, because "
            f"each becomes a security group and AWS attaches no more than "
            f"{MAX_NETWORKS_PER_SERVICE} to one network interface"
        )

    return networks


def _reject_unsupported_networks(app: DockerApplication) -> None:
    """External networks name infrastructure composey does not own."""
    external = sorted(
        name
        for name, definition in app.networks.items()
        if definition is not None and definition.external
    )
    if external:
        raise ValueError(
            f"networks {', '.join(external)} are declared external; composey "
            f"cannot map a network it does not create to a security group"
        )


def _settings_for(name: str, service: DockerService) -> XComposey:
    """Validate a service's x-composey block, naming the service if it is wrong."""
    try:
        return XComposey.model_validate(service.x_composey_raw)
    except ValidationError as error:
        raise ValueError(
            f"service {name!r} has an invalid x-composey block:\n{error}"
        ) from error


def normalize(app: DockerApplication, project_name: str) -> SemanticApplication:
    semantic_services = []
    relationships = []

    _reject_unsupported_networks(app)

    for s_name, docker_service in app.services.items():
        settings = _settings_for(s_name, docker_service)
        ingress = settings.exposure
        networks = _networks_for(s_name, docker_service, reserved=1 if ingress else 0)

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
        dockerfile = docker_service.build.dockerfile if docker_service.build else None

        # An explicit capability always beats what the image name suggests.
        if settings.capability is not None:
            capability = settings.capability

        schedule = _parse_schedule(settings.schedule) if settings.schedule else None

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
                build_context=build_context,
                dockerfile=dockerfile,
                command=command,
                startup_grace_period=settings.grace_period,
                min_scale=settings.min_scale,
                max_scale=settings.max_scale,
                schedule=schedule,
                cdn_enabled=settings.cdn,
                ingress=ingress,
                networks=networks,
                env=docker_service.environment,
                config=docker_service.platform_env,
                secrets=secret_names,
            )
        )

        # Build relationships
        for dep_name in docker_service.depends_on.keys():
            relationships.append(Relationship(client=s_name, server=dep_name))

    # Exposure is only ever declared. Deriving it from a published port made
    # publishing 80 mean "public" and publishing 8080 mean "unreachable", which
    # is not something a reader of the compose file could work out.
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
    """
    Two services cannot serve the same path: whichever rule the load balancer
    evaluated first would silently swallow the other's traffic.
    """
    seen: dict[str, str] = {}
    for service in services:
        if service.ingress is None:
            continue
        path = service.ingress.path
        if path in seen:
            raise ValueError(
                f"services {seen[path]!r} and {service.name!r} both serve "
                f"{path!r}; give each ingress a distinct path"
            )
        seen[path] = service.name
