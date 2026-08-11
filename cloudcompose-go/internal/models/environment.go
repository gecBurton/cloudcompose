package models

import "fmt"

// AwsEnvironment holds the AWS-specific environment configuration.
//
// Deliberately not a generic BaseEnvironment/AwsEnvironment hierarchy: Go
// has no direct equivalent of a discriminated-union model_validate, and
// this phase only needs the AWS shape. Azure/GCP environments have their
// own types alongside their own inference.
type AwsEnvironment struct {
	Target                  string            `json:"target"`
	Name                    string            `json:"name"`
	Region                  string            `json:"region"`
	LogRetentionDays        int               `json:"log_retention_days"`
	RetainDataOnDestroy     bool              `json:"retain_data_on_destroy"`
	HighAvailabilityEnabled bool              `json:"high_availability_enabled"`
	BackupRetentionDays     int               `json:"backup_retention_days"`
	Tags                    map[string]string `json:"tags,omitempty"`

	VpcID              string   `json:"vpc_id"`
	PublicSubnets      []string `json:"public_subnets"`
	PrivateSubnets     []string `json:"private_subnets"`
	EcsClusterArn      string   `json:"ecs_cluster_arn"`
	AlbArn             *string  `json:"alb_arn,omitempty"`
	AlbListenerArn     *string  `json:"alb_listener_arn,omitempty"`
	AlbSecurityGroupID *string  `json:"alb_security_group_id,omitempty"`
	AwsEndpoint        *string  `json:"aws_endpoint,omitempty"`
}

// NewAwsEnvironment returns an AwsEnvironment with default values
// (target="aws", region="us-east-1", log_retention_days=7,
// retain_data_on_destroy=true, high_availability_enabled=false,
// backup_retention_days=7).
//
// high_availability_enabled defaults off: RDS Multi-AZ roughly doubles
// compute cost for the standby, so it's an authored decision, not a
// silent default -- the same reasoning autoscaling's own default policy
// uses for opting a service INTO scaling only once max_scale>1 is
// declared, applied here to a cost-doubling decision instead. 7-day
// backup retention is RDS's own long-standing default (and the minimum
// Azure Flexible Server's backup_retention_days accepts), kept as the
// baseline here for both clouds rather than picking two different
// numbers with no reason to differ.
func NewAwsEnvironment() AwsEnvironment {
	return AwsEnvironment{
		Target:              "aws",
		Region:              "us-east-1",
		LogRetentionDays:    7,
		RetainDataOnDestroy: true,
		BackupRetentionDays: 7,
	}
}

// Validate enforces the one cross-field rule AwsEnvironment carries: a
// load balancer named without its security group leaves tasks with no way
// to accept its traffic except opening the port to the whole VPC, so the
// environment refuses to describe one without the other.
func (e *AwsEnvironment) Validate() error {
	if e.AlbArn != nil && e.AlbSecurityGroupID == nil {
		return fmt.Errorf("alb_security_group_id is required alongside alb_arn: without it " +
			"tasks would have to accept traffic from anywhere in the VPC " +
			"rather than from the load balancer alone")
	}
	return nil
}

// AzureEnvironment holds the Azure-specific environment configuration:
// Container Apps Environment, VNet, and Flexible Server configuration.
//
// resource_group_name and location for every Azure resource this
// application's inference creates come from Name and Region respectively
// (confirmed against every call site in compiler/azure/*.go:
// resource_group_name=env.name, location=env.region throughout) -- there
// is no separate resource-group field, unlike AWS's VpcID/subnets, which
// are their own fields because a VPC isn't named after the environment.
type AzureEnvironment struct {
	Target                  string            `json:"target"`
	Name                    string            `json:"name"`
	Region                  string            `json:"region"`
	LogRetentionDays        int               `json:"log_retention_days"`
	RetainDataOnDestroy     bool              `json:"retain_data_on_destroy"`
	HighAvailabilityEnabled bool              `json:"high_availability_enabled"`
	BackupRetentionDays     int               `json:"backup_retention_days"`
	Tags                    map[string]string `json:"tags,omitempty"`

	LogAnalyticsWorkspaceID string `json:"log_analytics_workspace_id"`
	ResourceGroupName       string `json:"resource_group_name"`
	VnetID                  string `json:"vnet_id"`
	VnetName                string `json:"vnet_name"`

	// AppsCIDR is the upper half of the environment's VNet, reserved
	// for apps -- see docs/azure-app-isolation-design.md's "Decided:
	// CIDR math" section. cloudcompose main carves its own /24 out of
	// this range (keyed by --subnet-index) for its own Container Apps
	// Environment's four subnets, rather than reading a
	// pre-created Container Apps Environment/subnet set the way
	// earlier revisions of this codebase did: a Container Apps
	// Environment is Azure's actual isolation boundary (confirmed
	// against the real azurerm_container_app schema, which has no
	// networking fields at all), so a shared one across apps would
	// defeat the isolation this design exists to provide.
	AppsCIDR string `json:"apps_cidr"`

	// SubnetIndex identifies this app's own /24 slice of AppsCIDR --
	// supplied fresh on every `cloudcompose main` invocation via the
	// --subnet-index flag, not decoded from the environment's own
	// Terraform outputs the way every other field above is (`json:"-"`
	// reflects that: LoadAzureEnvironment never sets this, and nothing
	// should expect GenerateAzureEnvironment's output block to carry
	// it either). Two apps sharing an index collide on the same subnet
	// range -- the same class of user error as two apps sharing a
	// --project name, not a new failure mode this field introduces.
	// See docs/azure-app-isolation-design.md's "Decided: per-app subnet
	// allocation" section for why this couldn't be automatic.
	SubnetIndex int `json:"-"`

	// InfrastructureSubnetID/PostgresqlSubnetID/MysqlSubnetID/
	// RedisSubnetID are likewise `json:"-"`: computed and set onto this
	// struct by InferAzure itself (azure/infer.go's appSubnetCIDRs),
	// from AppsCIDR + SubnetIndex, not decoded from
	// GenerateAzureEnvironment's output the way they were before
	// docs/azure-app-isolation-design.md's redesign moved subnet
	// creation from cloudcompose init to cloudcompose main. Every
	// consumer of these four fields (managed.go's
	// privateNetworkingAzure/privateEndpointRedisAzure) is unchanged --
	// only where the values come from moved.
	InfrastructureSubnetID string  `json:"-"`
	PostgresqlSubnetID     *string `json:"-"`
	MysqlSubnetID          *string `json:"-"`
	RedisSubnetID          *string `json:"-"`

	ContainerRegistryName  *string `json:"container_registry_name,omitempty"`
	PostgresqlServerID     *string `json:"postgresql_server_id,omitempty"`
	UserAssignedIdentityID *string `json:"user_assigned_identity_id,omitempty"`
	AzureEndpoint          *string `json:"azure_endpoint,omitempty"`
}

// NewAzureEnvironment returns an AzureEnvironment with default values
// (target="azure", region="eastus", log_retention_days=7,
// retain_data_on_destroy=true, high_availability_enabled=false,
// backup_retention_days=7). See NewAwsEnvironment's own doc comment for
// why HA defaults off and why 7 is the shared baseline retention.
func NewAzureEnvironment() AzureEnvironment {
	return AzureEnvironment{
		Target:              "azure",
		Region:              "eastus",
		LogRetentionDays:    7,
		RetainDataOnDestroy: true,
		BackupRetentionDays: 7,
	}
}

// GcpEnvironment holds the GCP-specific environment configuration: Cloud
// Run, VPC, and Cloud SQL configuration.
//
// Verified with lighter rigor than AwsEnvironment/AzureEnvironment: GCP
// has no golden examples and essentially no dedicated test suite, so
// this reflects the fields directly rather than being cross-checked
// against an existing verification surface the way the other two clouds
// were.
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

// NewGcpEnvironment returns a GcpEnvironment with default values
// (target="gcp", region="us-central1", log_retention_days=7,
// retain_data_on_destroy=true).
func NewGcpEnvironment() GcpEnvironment {
	return GcpEnvironment{
		Target:              "gcp",
		Region:              "us-central1",
		LogRetentionDays:    7,
		RetainDataOnDestroy: true,
	}
}
