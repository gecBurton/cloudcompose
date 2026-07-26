from typing import Annotated, Literal, Optional, Union

from pydantic import BaseModel, ConfigDict, Field

Capability = Literal["container", "database", "cache", "object-storage"]


class CronSchedule(BaseModel):
    """A recurring time, as a standard 5-field cron expression."""

    model_config = ConfigDict(extra="forbid")

    kind: Literal["cron"] = "cron"
    expression: str = Field(
        description="Standard 5-field cron: minute hour day-of-month month day-of-week"
    )


class RateSchedule(BaseModel):
    """A fixed interval between runs."""

    model_config = ConfigDict(extra="forbid")

    kind: Literal["rate"] = "rate"
    value: int = Field(gt=0, description="How many units between runs")
    unit: Literal["minutes", "hours", "days"] = Field(description="The interval unit")


# Kept cloud-neutral deliberately: the compose file must not have to carry a
# provider's scheduling dialect. Each backend renders its own (EventBridge
# wants a 6-field cron with a '?' placeholder, Azure wants standard 5-field).
Schedule = Annotated[Union[CronSchedule, RateSchedule], Field(discriminator="kind")]


class Ingress(BaseModel):
    """
    How a service is reached from outside.

    Replaces a single "is this public" boolean, which conflated four separate
    questions — reachable, on which port, at what path, and judged healthy how —
    and answered all of them by looking at whether a published port happened to
    be 80 or 443.
    """

    model_config = ConfigDict(extra="forbid")

    path: str = Field(
        default="/", description="URL prefix this service is served under"
    )
    port: Optional[int] = Field(
        default=None,
        description="Container port to route to. Defaults to the service's port.",
    )
    health_path: str = Field(
        default="/", description="Path polled to decide whether an instance is healthy"
    )
    priority: Optional[int] = Field(
        default=None,
        ge=1,
        le=50000,
        description="Rule evaluation order on a shared load balancer. Derived "
        "from the application and path when unset.",
    )


class Service(BaseModel):
    """
    A logical unit of compute or a managed capability.
    Cloud-agnostic representation of a process or stateful service.
    """

    model_config = ConfigDict(extra="forbid")

    name: str = Field(description="Unique identifier for the service")
    image: str = Field(description="Docker image URI")
    capability: Capability = Field(
        default="container",
        description="The nature of the service (Standard container or managed cloud service)",
    )
    size: Literal["small", "medium", "large"] = Field(
        default="small", description="The relative size of the compute resource"
    )
    cpu: Optional[int] = Field(default=None, description="CPU units (1024 = 1 vCPU)")
    memory: Optional[int] = Field(default=None, description="Memory in MiB")
    port: Optional[int] = Field(
        default=None, description="The internal port the service listens on"
    )
    build_context: Optional[str] = Field(
        default=None,
        description="Path (relative to the compose file) to a Docker build context. "
        "When set, the image is built and pushed to a registry instead of pulled.",
    )
    dockerfile: Optional[str] = Field(
        default=None,
        description="Dockerfile path relative to the build context",
    )
    command: Optional[list[str]] = Field(
        default=None, description="Container command override (exec form)"
    )
    startup_grace_period: Optional[int] = Field(
        default=None,
        description="Seconds a newly started instance is given to become healthy "
        "before health checks are enforced against it",
    )
    min_scale: int = Field(default=1, description="Minimum number of instances")
    max_scale: int = Field(default=1, description="Maximum number of instances")
    schedule: Optional[Schedule] = Field(
        default=None, description="When to run this service, if it is a scheduled task"
    )
    cdn_enabled: bool = Field(
        default=False, description="Whether to enable CDN for this service"
    )
    ingress: Optional[Ingress] = Field(
        default=None,
        description="How this service is reached from outside. None means it is "
        "internal: reachable by other services in the application, but not "
        "exposed.",
    )
    networks: list[str] = Field(
        default_factory=list,
        description="Networks this service is attached to. Two services can "
        "reach each other exactly when they share one, as in Compose.",
    )
    env: dict[str, str] = Field(
        default_factory=dict,
        description="Environment variables whose values the compose file states",
    )
    config: list[str] = Field(
        default_factory=list,
        description="Variables the service needs whose values the platform "
        "supplies. Named by the compose file, valued outside it.",
    )
    secrets: list[str] = Field(
        default_factory=list,
        description="List of secret names required by this service",
    )


class Connection(BaseModel):
    """
    How a client reaches one managed service.

    The attribute *vocabulary* is cloud-neutral; the values are target-specific
    expressions supplied by a backend. This exists so that wiring a client to a
    managed service is a structured substitution rather than guesswork about
    what a variable's name implies.
    """

    model_config = ConfigDict(extra="forbid")

    host: str = Field(description="Address the service is reached on")
    port: Optional[int] = Field(
        default=None,
        description="Port the service is reached on. None means the scheme's "
        "default applies and no port should be written.",
    )
    name: Optional[str] = Field(
        default=None,
        description="Identifier of the thing being addressed, where that "
        "differs from the host: a bucket, container or database name.",
    )
    addressed_by: Literal["host", "name"] = Field(
        default="host",
        description="Which attribute a bare reference to the service resolves "
        "to. A database is addressed by host; a bucket by name.",
    )

    @property
    def bare_reference(self) -> str:
        """The value a variable holding only the service's name resolves to."""
        if self.addressed_by == "name" and self.name is not None:
            return self.name
        return self.host


class Relationship(BaseModel):
    """
    Directed connectivity: client -> server.
    This is the single source of truth for network security and service discovery.
    """

    model_config = ConfigDict(extra="forbid")

    client: str = Field(description="The name of the service initiating the connection")
    server: str = Field(description="The name of the service receiving the connection")
    port: Optional[int] = Field(
        default=None,
        description="The specific port for this link. If None, uses the server's default port.",
    )


class Application(BaseModel):
    """
    The complete semantic representation of the application stack.
    """

    model_config = ConfigDict(extra="forbid")

    name: str = Field(description="The name of the application or environment")
    services: list[Service] = Field(
        default_factory=list, description="All compute nodes in the application"
    )
    relationships: list[Relationship] = Field(
        default_factory=list,
        description="Explicit list of all allowed network connections",
    )

    @property
    def public_services(self) -> list["Service"]:
        """Services reachable from outside. There may be any number."""
        return [s for s in self.services if s.ingress is not None]
