from typing import Dict, List, Literal, Optional, Type

import yaml
from pydantic import BaseModel, ConfigDict, Field, model_validator


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
    log_retention_days: int = Field(
        default=7,
        gt=0,
        description="How long application logs are kept. A platform policy, not an "
        "application choice. Ignored by targets whose log store belongs to the "
        "environment rather than to the compiled application.",
    )
    retain_data_on_destroy: bool = Field(
        default=True,
        description="Whether destroying the stack preserves data. True takes a "
        "final database snapshot, keeps secrets recoverable, and refuses to "
        "delete a non-empty bucket. False discards everything, which is what a "
        "throwaway test environment wants and nothing else does.",
    )
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
    alb_security_group_id: Optional[str] = Field(
        default=None,
        description="Security group of the shared load balancer. Tasks accept "
        "traffic from it and from nothing else.",
    )

    @model_validator(mode="after")
    def _load_balancer_is_fully_described(self) -> "AwsEnvironment":
        """
        A load balancer without its security group cannot be pointed at safely.

        Tasks have to accept the balancer's traffic somehow, and the only
        alternative to naming its group is opening the port to everything that
        can route to the subnet — every other application in the VPC included.
        """
        if self.alb_arn and not self.alb_security_group_id:
            raise ValueError(
                "alb_security_group_id is required alongside alb_arn: without it "
                "tasks would have to accept traffic from anywhere in the VPC "
                "rather than from the load balancer alone"
            )
        return self

    aws_endpoint: Optional[str] = Field(
        default=None,
        description="Optional custom endpoint for AWS services (e.g., for LocalStack)",
    )


class AzureEnvironment(BaseEnvironment):
    """
    Target context for Azure: Container Apps Environment, VNet, and
    Flexible Server configuration.
    """

    target: Literal["azure"] = "azure"
    region: str = Field(default="eastus", description="The Azure region")

    # Container Apps Environment
    container_apps_environment_name: str = Field(
        description="Name of the Container Apps Environment"
    )
    log_analytics_workspace_id: str = Field(
        description="Log Analytics Workspace ID for Container Apps"
    )

    # VNet Integration
    vnet_id: str = Field(description="The VNet ID for Container Apps integration")
    infrastructure_subnet_id: str = Field(
        description="Subnet ID for Container Apps infrastructure"
    )

    # A Flexible Server needs a subnet delegated to its own engine, so neither
    # database can reuse the Container Apps subnet and the two engines cannot
    # share one either. Optional so environment files written before composey
    # created these subnets stay loadable; a database then falls back to public
    # network access instead of failing to compile.
    postgresql_subnet_id: Optional[str] = Field(
        default=None,
        description="Subnet delegated to Microsoft.DBforPostgreSQL/flexibleServers",
    )
    mysql_subnet_id: Optional[str] = Field(
        default=None,
        description="Subnet delegated to Microsoft.DBforMySQL/flexibleServers",
    )

    # Container Registry (ACR)
    container_registry_name: Optional[str] = Field(
        default=None,
        description="Azure Container Registry name for built images",
    )

    # PostgreSQL Flexible Server (optional - creates new if not provided)
    postgresql_server_id: Optional[str] = Field(
        default=None,
        description="Existing PostgreSQL Flexible Server ID (creates new if not set)",
    )

    # Managed Identity (system-assigned is default, but user-assigned can be specified)
    user_assigned_identity_id: Optional[str] = Field(
        default=None,
        description="User-assigned managed identity ID (uses system-assigned if not set)",
    )

    azure_endpoint: Optional[str] = Field(
        default=None,
        description="Optional custom endpoint for Azure APIs (e.g., for testing)",
    )


class GcpEnvironment(BaseEnvironment):
    """
    Target context for GCP: Cloud Run, VPC, and Cloud SQL configuration.
    """

    target: Literal["gcp"] = "gcp"
    region: str = Field(default="us-central1", description="The GCP region")
    project_id: str = Field(description="The GCP project ID")

    # VPC Configuration
    vpc_id: Optional[str] = Field(
        default=None,
        description="VPC ID for Cloud Run VPC access (optional - uses default if not set)",
    )
    subnet_ids: Optional[List[str]] = Field(
        default=None,
        description="Subnet IDs for VPC connector (optional)",
    )

    # Cloud SQL (optional - creates new if not provided)
    cloud_sql_instance_id: Optional[str] = Field(
        default=None,
        description="Existing Cloud SQL instance ID (creates new if not set)",
    )

    # Artifact Registry (optional)
    artifact_registry_repository: Optional[str] = Field(
        default=None,
        description="Artifact Registry repository name for built images",
    )

    # Load Balancer / Cloud CDN (optional)
    load_balancer_ip: Optional[str] = Field(
        default=None,
        description="Existing global static IP for load balancer (creates new if not set)",
    )

    # Service Account (optional - uses default compute service account if not set)
    service_account_email: Optional[str] = Field(
        default=None,
        description="Service account email for Cloud Run (uses default if not set)",
    )

    gcp_endpoint: Optional[str] = Field(
        default=None,
        description="Optional custom endpoint for GCP APIs (e.g., for testing)",
    )


# Every supported compilation target, keyed by the `target` field of its
# environment file. Adding a cloud means adding an entry here.
TARGETS: Dict[str, Type[BaseEnvironment]] = {
    "aws": AwsEnvironment,
    "azure": AzureEnvironment,
    "gcp": GcpEnvironment,
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
