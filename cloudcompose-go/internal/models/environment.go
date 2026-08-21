package models

import "fmt"

// AwsEnvironment holds the AWS-specific environment configuration.
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

	// Backend is this environment's own backend config, read back from
	// its own `output "backend"` block; nil if generated without
	// backend: configured. Every app compiled against this environment
	// reuses this same bucket/region/lock-table under its own key.
	Backend *BackendConfig `json:"-"`
}

// NewAwsEnvironment returns an AwsEnvironment with default values
// (target="aws", region="us-east-1", log_retention_days=7,
// retain_data_on_destroy=true, high_availability_enabled=false,
// backup_retention_days=7).
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
// to accept its traffic except opening the port to the whole VPC.
func (e *AwsEnvironment) Validate() error {
	if e.AlbArn != nil && e.AlbSecurityGroupID == nil {
		return fmt.Errorf("alb_security_group_id is required alongside alb_arn: without it " +
			"tasks would have to accept traffic from anywhere in the VPC " +
			"rather than from the load balancer alone")
	}
	return nil
}

// NewDemoAwsEnvironment returns a fully-populated AwsEnvironment with
// plausible-looking placeholder values, for `cloudcompose main --demo aws`.
func NewDemoAwsEnvironment() AwsEnvironment {
	env := NewAwsEnvironment()
	env.Name = "demo"
	env.VpcID = "vpc-demo0123456789"
	env.PublicSubnets = []string{"subnet-demo1", "subnet-demo2"}
	env.PrivateSubnets = []string{"subnet-demo3", "subnet-demo4"}
	env.EcsClusterArn = "arn:aws:ecs:us-east-1:000000000000:cluster/demo-cluster"
	albArn := "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/demo-alb/0123456789abcdef"
	albListenerArn := albArn + ":listener/0123456789abcdef"
	albSG := "sg-demo0123456789"
	env.AlbArn = &albArn
	env.AlbListenerArn = &albListenerArn
	env.AlbSecurityGroupID = &albSG
	return env
}

// AzureEnvironment holds the Azure-specific environment configuration:
// Container Apps Environment, VNet, and Flexible Server configuration.
//
// resource_group_name and location for every Azure resource come from
// Name and Region respectively; there is no separate resource-group
// field.
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
	// for apps. cloudcompose main carves its own /24 out of this range
	// (keyed by --subnet-index) for its own Container Apps Environment's
	// four subnets.
	AppsCIDR string `json:"apps_cidr"`

	// SubnetIndex identifies this app's own /24 slice of AppsCIDR --
	// supplied fresh on every `cloudcompose main` invocation via the
	// --subnet-index flag, not decoded from Terraform outputs. Two apps
	// sharing an index collide on the same subnet range.
	SubnetIndex int `json:"-"`

	// InfrastructureSubnetID/PostgresqlSubnetID/MysqlSubnetID/
	// RedisSubnetID are computed and set onto this struct by InferAzure
	// itself, from AppsCIDR + SubnetIndex, not decoded from Terraform
	// output.
	InfrastructureSubnetID string  `json:"-"`
	PostgresqlSubnetID     *string `json:"-"`
	MysqlSubnetID          *string `json:"-"`
	RedisSubnetID          *string `json:"-"`

	ContainerRegistryName  *string `json:"container_registry_name,omitempty"`
	PostgresqlServerID     *string `json:"postgresql_server_id,omitempty"`
	UserAssignedIdentityID *string `json:"user_assigned_identity_id,omitempty"`
	AzureEndpoint          *string `json:"azure_endpoint,omitempty"`

	// Backend mirrors AwsEnvironment.Backend.
	Backend *BackendConfig `json:"-"`
}

// NewAzureEnvironment returns an AzureEnvironment with default values
// (target="azure", region="eastus", log_retention_days=7,
// retain_data_on_destroy=true, high_availability_enabled=false,
// backup_retention_days=7).
func NewAzureEnvironment() AzureEnvironment {
	return AzureEnvironment{
		Target:              "azure",
		Region:              "eastus",
		LogRetentionDays:    7,
		RetainDataOnDestroy: true,
		BackupRetentionDays: 7,
	}
}

// NewDemoAzureEnvironment returns a fully-populated AzureEnvironment with
// plausible-looking placeholder values, for `cloudcompose main --demo
// azure`. InfrastructureSubnetID/PostgresqlSubnetID/MysqlSubnetID/
// RedisSubnetID are deliberately left unset: InferAzure computes them
// itself from AppsCIDR + SubnetIndex.
func NewDemoAzureEnvironment() AzureEnvironment {
	env := NewAzureEnvironment()
	env.Name = "demo"
	env.ResourceGroupName = "demo"
	env.LogAnalyticsWorkspaceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/demo/providers/Microsoft.OperationalInsights/workspaces/demo-logs"
	env.VnetID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/demo/providers/Microsoft.Network/virtualNetworks/demo-vnet"
	env.VnetName = "demo-vnet"
	env.AppsCIDR = "10.0.128.0/17"
	env.SubnetIndex = 0
	registryName := "demoacr"
	env.ContainerRegistryName = &registryName
	return env
}

// GcpEnvironment holds the GCP-specific environment configuration: Cloud
// Run, VPC, and Cloud SQL configuration.
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
	// under. Unlike AWS/Azure, a Google-managed SSL certificate cannot
	// be issued without a domain the caller owns. Not yet consumed by
	// inference.
	Domain *string `json:"domain,omitempty"`

	// Backend mirrors AwsEnvironment.Backend. GcpBackendConfig has no
	// lock-table-equivalent field: GCS backend locking is native.
	Backend *BackendConfig `json:"-"`
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

// NewDemoGcpEnvironment returns a fully-populated GcpEnvironment with a
// placeholder project ID, for `cloudcompose main --demo gcp`.
func NewDemoGcpEnvironment() GcpEnvironment {
	env := NewGcpEnvironment()
	env.Name = "demo"
	env.ProjectID = "demo-project-000000"
	return env
}
