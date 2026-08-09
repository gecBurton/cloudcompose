package models

import "fmt"

// AwsEnvironment mirrors composey/models/environment.py's AwsEnvironment.
//
// Deliberately not a generic BaseEnvironment/AwsEnvironment hierarchy: Go
// has no direct equivalent of Pydantic's discriminated-union model_validate,
// and this phase only needs the AWS shape. Azure/GCP environments are ported
// in Phase 4 alongside their own inference.
type AwsEnvironment struct {
	Target              string            `json:"target"`
	Name                string            `json:"name"`
	Region              string            `json:"region"`
	LogRetentionDays    int               `json:"log_retention_days"`
	RetainDataOnDestroy bool              `json:"retain_data_on_destroy"`
	Tags                map[string]string `json:"tags,omitempty"`

	VpcID              string   `json:"vpc_id"`
	PublicSubnets      []string `json:"public_subnets"`
	PrivateSubnets     []string `json:"private_subnets"`
	EcsClusterArn      string   `json:"ecs_cluster_arn"`
	AlbArn             *string  `json:"alb_arn,omitempty"`
	AlbListenerArn     *string  `json:"alb_listener_arn,omitempty"`
	AlbSecurityGroupID *string  `json:"alb_security_group_id,omitempty"`
	AwsEndpoint        *string  `json:"aws_endpoint,omitempty"`
}

// NewAwsEnvironment returns an AwsEnvironment with the same field defaults
// Pydantic applies in environment.py (target="aws", region="us-east-1",
// log_retention_days=7, retain_data_on_destroy=true).
func NewAwsEnvironment() AwsEnvironment {
	return AwsEnvironment{
		Target:              "aws",
		Region:              "us-east-1",
		LogRetentionDays:    7,
		RetainDataOnDestroy: true,
	}
}

// Validate enforces the one cross-field rule AwsEnvironment carries in
// Python: a load balancer named without its security group leaves tasks
// with no way to accept its traffic except opening the port to the whole
// VPC, so the environment refuses to describe one without the other.
func (e *AwsEnvironment) Validate() error {
	if e.AlbArn != nil && e.AlbSecurityGroupID == nil {
		return fmt.Errorf("alb_security_group_id is required alongside alb_arn: without it " +
			"tasks would have to accept traffic from anywhere in the VPC " +
			"rather than from the load balancer alone")
	}
	return nil
}

// AzureEnvironment mirrors composey/models/environment.py's
// AzureEnvironment: Container Apps Environment, VNet, and Flexible Server
// configuration.
//
// resource_group_name and location for every Azure resource this
// application's inference creates come from Name and Region respectively
// (confirmed against every call site in compiler/inference/azure/__init__.py:
// resource_group_name=env.name, location=env.region throughout) -- there
// is no separate resource-group field, unlike AWS's VpcID/subnets, which
// are their own fields because a VPC isn't named after the environment.
type AzureEnvironment struct {
	Target              string            `json:"target"`
	Name                string            `json:"name"`
	Region              string            `json:"region"`
	LogRetentionDays    int               `json:"log_retention_days"`
	RetainDataOnDestroy bool              `json:"retain_data_on_destroy"`
	Tags                map[string]string `json:"tags,omitempty"`

	ContainerAppsEnvironmentName string `json:"container_apps_environment_name"`
	LogAnalyticsWorkspaceID      string `json:"log_analytics_workspace_id"`
	VnetID                       string `json:"vnet_id"`
	InfrastructureSubnetID       string `json:"infrastructure_subnet_id"`

	// A Flexible Server needs a subnet delegated to its own engine, so
	// neither database can reuse the Container Apps subnet and the two
	// engines cannot share one either. Optional so environment files
	// written before composey created these subnets stay loadable; a
	// database then falls back to public network access instead of
	// failing to compile.
	PostgresqlSubnetID *string `json:"postgresql_subnet_id,omitempty"`
	MysqlSubnetID      *string `json:"mysql_subnet_id,omitempty"`

	// RedisSubnetID is where a Managed Redis instance's
	// azurerm_private_endpoint is placed, added 2026-08-08 (see
	// docs/azure-aws-parity-todo.md's Priority 3 Redis/Blob private
	// networking item). Unlike Postgresql/MysqlSubnetID above, this
	// doesn't need to be a *delegated* subnet the way Flexible Server's
	// requires -- a private endpoint attaches to any plain subnet -- but
	// it's kept as its own field rather than reusing
	// InfrastructureSubnetID (the Container Apps subnet) since Azure
	// does not allow a private endpoint and delegated Container Apps
	// workloads to share one subnet either. Same optional/fallback
	// convention as the two fields above: nil means fall back to public
	// network access rather than failing to compile.
	RedisSubnetID *string `json:"redis_subnet_id,omitempty"`

	ContainerRegistryName  *string `json:"container_registry_name,omitempty"`
	PostgresqlServerID     *string `json:"postgresql_server_id,omitempty"`
	UserAssignedIdentityID *string `json:"user_assigned_identity_id,omitempty"`
	AzureEndpoint          *string `json:"azure_endpoint,omitempty"`
}

// NewAzureEnvironment returns an AzureEnvironment with the same field
// defaults Pydantic applies in environment.py (target="azure",
// region="eastus", log_retention_days=7, retain_data_on_destroy=true).
func NewAzureEnvironment() AzureEnvironment {
	return AzureEnvironment{
		Target:              "azure",
		Region:              "eastus",
		LogRetentionDays:    7,
		RetainDataOnDestroy: true,
	}
}

// GcpEnvironment mirrors composey/models/environment.py's GcpEnvironment:
// Cloud Run, VPC, and Cloud SQL configuration.
//
// Ported with lighter verification than AwsEnvironment/AzureEnvironment:
// GCP has no golden examples and essentially no dedicated test suite in
// Python either (see plan.md's Phase 4 GCP section), so this reflects the
// Python model's fields directly rather than being cross-checked against
// an existing verification surface the way the other two clouds were.
type GcpEnvironment struct {
	Target              string            `json:"target"`
	Name                string            `json:"name"`
	Region              string            `json:"region"`
	LogRetentionDays    int               `json:"log_retention_days"`
	RetainDataOnDestroy bool              `json:"retain_data_on_destroy"`
	Tags                map[string]string `json:"tags,omitempty"`

	ProjectID string `json:"project_id"`

	VpcID     *string  `json:"vpc_id,omitempty"`
	SubnetIDs []string `json:"subnet_ids,omitempty"`

	CloudSqlInstanceID         *string `json:"cloud_sql_instance_id,omitempty"`
	ArtifactRegistryRepository *string `json:"artifact_registry_repository,omitempty"`
	LoadBalancerIP             *string `json:"load_balancer_ip,omitempty"`
	ServiceAccountEmail        *string `json:"service_account_email,omitempty"`
	GcpEndpoint                *string `json:"gcp_endpoint,omitempty"`

	// Domain is the custom domain a CDN-enabled service should be served
	// under. Unlike AWS/Azure (which get a free CloudFront/Front Door
	// hostname), a Google-managed SSL certificate cannot be issued
	// without a domain the caller owns -- see
	// docs/spikes/gcp/README.md's "cdn: true is not self-sufficient on
	// GCP". Not yet consumed by inference (gcp/infer.go's load-balancer
	// step is still a documented no-op); this field exists so the
	// authored environment.yaml schema has somewhere to put the decision
	// once that inference is implemented, rather than that being blocked
	// on a schema change too.
	Domain *string `json:"domain,omitempty"`
}

// NewGcpEnvironment returns a GcpEnvironment with the same field defaults
// Pydantic applies in environment.py (target="gcp", region="us-central1",
// log_retention_days=7, retain_data_on_destroy=true).
func NewGcpEnvironment() GcpEnvironment {
	return GcpEnvironment{
		Target:              "gcp",
		Region:              "us-central1",
		LogRetentionDays:    7,
		RetainDataOnDestroy: true,
	}
}
