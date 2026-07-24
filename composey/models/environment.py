from typing import Dict, List, Literal, Optional, Type

import yaml
from pydantic import BaseModel, ConfigDict, Field


class BaseEnvironment(BaseModel):
    """
    Infrastructure context owned by the platform team.

    Holds only what every cloud has in common. The `target` field selects the
    concrete subclass that carries the provider-specific context, so that a
    compose file can be compiled against any supported cloud.
    """

    model_config = ConfigDict(extra="forbid")

    target: str = Field(description="The cloud the application is deployed to")
    name: str = Field(description="Environment name (e.g., production, staging)")
    region: str = Field(description="The region to deploy into")
    tags: Optional[Dict[str, str]] = Field(
        default=None, description="Default tags for all resources"
    )


class AwsEnvironment(BaseEnvironment):
    """
    Target context for AWS: an existing VPC, ECS cluster and (optionally) a
    shared Application Load Balancer.
    """

    # Defaulted so environment files written before composey supported more
    # than one cloud stay valid.
    target: Literal["aws"] = "aws"
    region: str = Field(default="us-east-1", description="The AWS region")
    vpc_id: str = Field(description="The VPC ID")
    public_subnets: List[str] = Field(description="List of public subnet IDs for ALBs")
    private_subnets: List[str] = Field(
        description="List of private subnet IDs for tasks"
    )
    ecs_cluster_arn: str = Field(description="The ARN of the ECS Cluster")
    alb_arn: Optional[str] = Field(
        default=None, description="The ARN of the shared Application Load Balancer"
    )
    alb_listener_arn: Optional[str] = Field(
        default=None, description="The ARN of the HTTPS/HTTP listener on the ALB"
    )
    aws_endpoint: Optional[str] = Field(
        default=None,
        description="Optional custom endpoint for AWS services (e.g., for LocalStack)",
    )


# Every supported compilation target, keyed by the `target` field of its
# environment file. Adding a cloud means adding an entry here.
TARGETS: Dict[str, Type[BaseEnvironment]] = {
    "aws": AwsEnvironment,
}

DEFAULT_TARGET = "aws"


def load_environment(path: str) -> BaseEnvironment:
    """
    Load an environment file, validating it against its declared target.
    """
    with open(path, "r") as f:
        data = yaml.safe_load(f)

    if not isinstance(data, dict):
        raise ValueError(f"{path} does not contain a mapping of environment settings")

    target = data.get("target", DEFAULT_TARGET)
    if target not in TARGETS:
        supported = ", ".join(sorted(TARGETS))
        raise ValueError(
            f"{path} declares unsupported target {target!r}. Supported targets: {supported}."
        )

    return TARGETS[target].model_validate(data)
