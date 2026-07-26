from typing import Literal, Optional, Union

from pydantic import BaseModel, ConfigDict, Field


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
    secrets: Optional[list[Union[str, SecretDefinition]]] = Field(default=None)
    volumes: Optional[list[Union[str, VolumeDefinition]]] = Field(default=None)

    @property
    def x_composey(self) -> dict:
        return (self.model_extra or {}).get("x-composey") or {}


class Application(BaseModel):
    model_config = {"extra": "ignore"}

    services: dict[str, Service]
