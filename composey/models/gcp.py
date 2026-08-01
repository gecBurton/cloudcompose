"""GCP resource models for Terraform generation.

These models represent GCP resources that the compiler can generate.
Each model maps to a Google Terraform provider resource.
"""

from typing import Any, Dict, List, Optional

from pydantic import BaseModel, ConfigDict, Field


class CloudRunService(BaseModel):
    """Cloud Run service resource."""

    model_config = ConfigDict(extra="forbid")

    name: str
    location: str
    project_id: str

    # Container configuration
    template: Dict[str, Any] = Field(
        default_factory=dict,
        description="Service template with containers, scaling, etc.",
    )

    # Traffic configuration
    traffic: Optional[List[Dict[str, Any]]] = Field(
        default=None,
        description="Traffic routing configuration",
    )

    # VPC access
    vpc_access: Optional[Dict[str, Any]] = Field(
        default=None,
        description="VPC connector configuration",
    )

    # IAM
    autogenerate_revision_name: bool = True

    # Ingress settings
    ingress: str = Field(
        default="all",
        description="Ingress settings: all, internal, internal-and-cloud-load-balancing",
    )

    # URL handling
    depends_on: Optional[List[str]] = None


class CloudRunServiceIamMember(BaseModel):
    """IAM binding for Cloud Run service."""

    model_config = ConfigDict(extra="forbid")

    service: str  # Reference to cloud run service
    location: str
    project_id: str
    role: str
    member: str


class CloudSqlInstance(BaseModel):
    """Cloud SQL instance resource."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str
    region: str

    # Database version
    database_version: str = Field(
        default="POSTGRES_14",
        description="POSTGRES_14, MYSQL_8_0, etc.",
    )

    # Tier / machine type
    tier: str = Field(
        default="db-f1-micro",
        description="Machine type (db-f1-micro, db-g1-small, etc.)",
    )

    # Storage
    storage_auto_resize: bool = True
    storage_auto_resize_limit: int = Field(default=100, description="GB")

    # High availability
    availability_type: str = Field(
        default="ZONAL",
        description="ZONAL or REGIONAL",
    )

    # Backups
    backup_configuration: Optional[Dict[str, Any]] = Field(
        default_factory=lambda: {"enabled": True, "start_time": "03:00"},
    )

    # IP configuration
    ip_configuration: Dict[str, Any] = Field(
        default_factory=lambda: {
            "ipv4_enabled": True,
            "private_network": None,
        },
    )

    # Root password
    root_password: Optional[str] = None

    # Database to create
    database_name: Optional[str] = None


class CloudSqlDatabase(BaseModel):
    """Database within a Cloud SQL instance."""

    model_config = ConfigDict(extra="forbid")

    name: str
    instance: str  # Reference to cloud_sql_instance
    project_id: str


class RedisInstance(BaseModel):
    """Memorystore Redis instance resource."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str
    region: str

    # Tier
    tier: str = Field(
        default="BASIC",
        description="BASIC, STANDARD_HA",
    )

    # Memory size
    memory_size_gb: int = Field(default=1, ge=1, le=300)

    # Redis version
    redis_version: str = Field(default="REDIS_6_X")

    # Network
    authorized_network: Optional[str] = None
    connect_mode: str = "DIRECT_PEERING"


class StorageBucket(BaseModel):
    """Cloud Storage bucket resource."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str
    location: str = Field(default="US")

    # Storage class
    storage_class: str = Field(default="STANDARD")

    # Versioning
    versioning: Dict[str, bool] = Field(default_factory=lambda: {"enabled": False})

    # Lifecycle rules
    lifecycle_rule: Optional[List[Dict[str, Any]]] = None

    # Uniform bucket-level access
    uniform_bucket_level_access: bool = True

    # Force destroy for cleanup
    force_destroy: bool = False


class StorageBucketIamMember(BaseModel):
    """IAM binding for Storage bucket."""

    model_config = ConfigDict(extra="forbid")

    bucket: str
    role: str
    member: str


class VpcConnector(BaseModel):
    """VPC Access connector for Cloud Run."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str
    region: str

    # Network config
    network: str = "default"
    ip_cidr_range: str = Field(default="10.8.0.0/28")

    # Machine type
    machine_type: str = Field(default="f1-micro")

    # Min/max instances
    min_instances: int = 2
    max_instances: int = 10


class GlobalAddress(BaseModel):
    """Global static IP address for load balancing."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str

    address_type: str = "EXTERNAL"
    ip_version: str = "IPV4"


class ComputeManagedSslCertificate(BaseModel):
    """Managed SSL certificate for load balancing."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str

    managed: Dict[str, List[str]] = Field(default_factory=lambda: {"domains": []})


class ComputeUrlMap(BaseModel):
    """URL map for load balancing."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str

    default_service: str  # Reference to backend service
    host_rule: Optional[List[Dict[str, Any]]] = None
    path_matcher: Optional[List[Dict[str, Any]]] = None


class ComputeBackendService(BaseModel):
    """Backend service for load balancing."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str

    backend: List[Dict[str, Any]]

    # Cloud Run can be a backend
    # Using NEG (Network Endpoint Group)


class ComputeTargetHttpsProxy(BaseModel):
    """HTTPS proxy for load balancing."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str

    url_map: str
    ssl_certificates: List[str]


class ComputeForwardingRule(BaseModel):
    """Forwarding rule for load balancing."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str

    target: str  # Reference to HTTPS proxy
    ip_address: Optional[str] = None
    port_range: str = "443"


class ComputeRegionNetworkEndpointGroup(BaseModel):
    """Network endpoint group for Cloud Run."""

    model_config = ConfigDict(extra="forbid")

    name: str
    project_id: str
    region: str

    network_endpoint_type: str = "SERVERLESS"
    cloud_run: Dict[str, str]  # service name


class SecretManagerSecret(BaseModel):
    """Secret Manager secret resource."""

    model_config = ConfigDict(extra="forbid")

    secret_id: str
    project_id: str

    replication: Dict[str, Any] = Field(default_factory=lambda: {"automatic": {}})


class SecretManagerSecretVersion(BaseModel):
    """Secret version resource."""

    model_config = ConfigDict(extra="forbid")

    secret: str  # Reference to secret
    secret_data: str


class GcpResources(BaseModel):
    """A registry of the GCP resources our compiler supports."""

    # Cloud Run
    google_cloud_run_service: Dict[str, CloudRunService] = Field(default_factory=dict)
    google_cloud_run_service_iam_member: Dict[str, CloudRunServiceIamMember] = Field(
        default_factory=dict
    )

    # Cloud SQL
    google_sql_database_instance: Dict[str, CloudSqlInstance] = Field(
        default_factory=dict
    )
    google_sql_database: Dict[str, CloudSqlDatabase] = Field(default_factory=dict)

    # Memorystore (Redis)
    google_redis_instance: Dict[str, RedisInstance] = Field(default_factory=dict)

    # Cloud Storage
    google_storage_bucket: Dict[str, StorageBucket] = Field(default_factory=dict)
    google_storage_bucket_iam_member: Dict[str, StorageBucketIamMember] = Field(
        default_factory=dict
    )

    # VPC
    google_vpc_access_connector: Dict[str, VpcConnector] = Field(default_factory=dict)

    # Load Balancing (for CDN/custom domains)
    google_compute_global_address: Dict[str, GlobalAddress] = Field(
        default_factory=dict
    )
    google_compute_managed_ssl_certificate: Dict[str, ComputeManagedSslCertificate] = (
        Field(default_factory=dict)
    )
    google_compute_region_network_endpoint_group: Dict[
        str, ComputeRegionNetworkEndpointGroup
    ] = Field(default_factory=dict)
    google_compute_backend_service: Dict[str, ComputeBackendService] = Field(
        default_factory=dict
    )
    google_compute_url_map: Dict[str, ComputeUrlMap] = Field(default_factory=dict)
    google_compute_target_https_proxy: Dict[str, ComputeTargetHttpsProxy] = Field(
        default_factory=dict
    )
    google_compute_forwarding_rule: Dict[str, ComputeForwardingRule] = Field(
        default_factory=dict
    )

    # Secret Manager
    google_secret_manager_secret: Dict[str, SecretManagerSecret] = Field(
        default_factory=dict
    )
    google_secret_manager_secret_version: Dict[str, SecretManagerSecretVersion] = Field(
        default_factory=dict
    )

    # Docker provider resources
    docker_image: Dict[str, Any] = Field(default_factory=dict)
    docker_registry_image: Dict[str, Any] = Field(default_factory=dict)

    # Random resources for passwords
    random_password: Dict[str, Any] = Field(default_factory=dict)
