from typing import Literal, Optional, Union

from pydantic import BaseModel, ConfigDict, Field, model_validator

from .semantic import Capability, Ingress


class Port(BaseModel):
    mode: Literal["ingress", "egress"] = "ingress"
    target: int
    published: Optional[int] = None
    protocol: Literal["tcp", "udp"] = "tcp"


class Build(BaseModel):
    """
    docker-compose build

    `context` is expected to be relative to the compose file. The parser makes
    it so; taking the basename of an absolute path (as this model used to) is
    wrong whenever the context is not directly beside the compose file, which
    is the normal case in a monorepo where several services build from the root.
    """

    context: str = Field(description="context")
    dockerfile: Optional[str] = Field(
        default=None, description="Dockerfile path, relative to the context"
    )


class Dependency(BaseModel):
    condition: str = Field(description="condition")
    required: bool = Field(description="required", default=True)


class SecretDefinition(BaseModel):
    source: str
    target: Optional[str] = None
    uid: Optional[str] = None
    gid: Optional[str] = None
    mode: Optional[int] = None


class VolumeDefinition(BaseModel):
    type: str
    source: Optional[str] = None
    target: str
    read_only: bool = False


class HealthCheckConfig(BaseModel):
    """Health check configuration for x-composey."""

    model_config = ConfigDict(extra="forbid")

    type: Literal["http", "tcp"] = Field(default="http")
    path: str = Field(default="/")
    port: Optional[int] = Field(default=None)


class AutoScalingMetricConfig(BaseModel):
    """Auto-scaling metric configuration for x-composey."""

    model_config = ConfigDict(extra="forbid")

    type: Literal["cpu", "memory", "requests_per_target"] = Field(
        default="cpu",
        description="Metric to scale on",
    )
    target: float = Field(
        default=70.0,
        gt=0,
        le=100,
        description="Target value (percentage for cpu/memory, count for requests)",
    )


class AutoScalingConfig(BaseModel):
    """Auto-scaling configuration for x-composey."""

    model_config = ConfigDict(extra="forbid")

    metrics: list[AutoScalingMetricConfig] = Field(
        default_factory=lambda: [
            AutoScalingMetricConfig(type="cpu", target=70.0),
            AutoScalingMetricConfig(type="memory", target=80.0),
        ],
    )
    scale_in_cooldown: int = Field(default=300, ge=0)
    scale_out_cooldown: int = Field(default=60, ge=0)


class XComposey(BaseModel):
    """
    The `x-composey` block on a service.

    Validated with `extra="forbid"` on purpose. An override you can misspell is
    not an override: before this existed, `capabilty: database` was silently
    dropped and the service deployed as whatever the compiler guessed.
    """

    model_config = ConfigDict(extra="forbid")

    capability: Optional[Capability] = Field(
        default=None,
        description="What this service really is, when the image name does not say",
    )
    ingress: Optional[Ingress] = Field(
        default=None,
        description="How this service is reached from outside: path, port, health",
    )
    health_check: Optional[HealthCheckConfig] = Field(
        default=None,
        description="Health check configuration. Defaults to HTTP / on the ingress port.",
    )
    auto_scaling: Optional[AutoScalingConfig] = Field(
        default=None,
        description="Auto-scaling configuration. When min_scale != max_scale, "
        "auto-scaling is enabled. Defaults to CPU 70% and Memory 80%.",
    )

    @model_validator(mode="before")
    @classmethod
    def _bare_ingress_means_defaults(cls, data):
        """
        `ingress:` with nothing under it declares a default route.

        Without this it parses as null and the service is quietly internal —
        reintroducing, at the only place it still could, exactly the silent
        non-exposure this design exists to prevent.
        """
        if isinstance(data, dict) and "ingress" in data and data["ingress"] is None:
            data = {**data, "ingress": {}}
        return data

    @property
    def exposure(self) -> Optional[Ingress]:
        """The route this service declares, if any."""
        return self.ingress

    size: Literal["small", "medium", "large"] = Field(default="small")
    cpu: Optional[int] = Field(default=None, gt=0)
    memory: Optional[int] = Field(default=None, gt=0)
    min_scale: int = Field(default=1, ge=0)
    max_scale: int = Field(default=1, ge=1)
    schedule: Optional[str] = Field(default=None)
    cdn: bool = Field(default=False)
    startup_grace_period: Optional[int] = Field(default=None, ge=0)
    health_check_grace_period: Optional[int] = Field(
        default=None, ge=0, description="Deprecated spelling of startup_grace_period"
    )

    @property
    def grace_period(self) -> Optional[int]:
        """The startup grace period under either spelling."""
        if self.startup_grace_period is not None:
            return self.startup_grace_period
        return self.health_check_grace_period


class Service(BaseModel):
    """
    docker-compose service
    """

    model_config = ConfigDict(populate_by_name=True, extra="allow")

    build: Optional[Build] = Field(description="build", default=None)
    ports: Optional[list[Port]] = Field(description="ports", default=None)
    image: Optional[str] = Field(description="image", default=None)
    command: Optional[Union[str, list[str]]] = Field(
        description="command", default=None
    )
    environment: dict[str, Optional[str]] = Field(
        description="environment", default_factory=dict
    )
    depends_on: dict[str, Dependency] = Field(default_factory=dict)
    platform_env: list[str] = Field(
        default_factory=list,
        description="Variables the service needs whose values come from the "
        "platform rather than the compose file. Populated by the parser.",
    )
    networks: Optional[dict[str, Optional[dict]]] = Field(
        default=None,
        description="Networks this service joins. `docker compose config` always "
        "resolves this, materialising `default` when the file declares none.",
    )
    secrets: Optional[list[Union[str, SecretDefinition]]] = Field(default=None)
    volumes: Optional[list[Union[str, VolumeDefinition]]] = Field(default=None)

    @property
    def x_composey_raw(self) -> dict:
        return (self.model_extra or {}).get("x-composey") or {}


class NetworkDefinition(BaseModel):
    model_config = ConfigDict(extra="allow")

    name: Optional[str] = None
    external: bool = False


class Application(BaseModel):
    model_config = {"extra": "ignore"}

    services: dict[str, Service]
    networks: dict[str, Optional[NetworkDefinition]] = Field(default_factory=dict)
