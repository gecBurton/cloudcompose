package models

// InitConfig is the authored input to `cloudcompose init`: the decisions a
// human makes about a shared environment before any infrastructure
// exists, as opposed to the facts Terraform assigns once that
// infrastructure is created (those live in the generated
// Aws/Azure/GcpEnvironment models in environment.go).
//
// Exactly one of AWS/Azure/Gcp may be set, and it must match Provider --
// enforced by the loader (internal/compiler/initconfig), not by this
// struct itself.
type InitConfig struct {
	Provider            string            `yaml:"provider"`
	Name                string            `yaml:"name"`
	Region              string            `yaml:"region,omitempty"`
	Tags                map[string]string `yaml:"tags,omitempty"`
	RetainDataOnDestroy *bool             `yaml:"retain_data_on_destroy,omitempty"`

	// HighAvailabilityEnabled/BackupRetentionDays are common-envelope:
	// applied uniformly to every database this environment's apps
	// create, regardless of cloud. Not yet wired for GCP.
	HighAvailabilityEnabled *bool `yaml:"high_availability_enabled,omitempty"`
	BackupRetentionDays     *int  `yaml:"backup_retention_days,omitempty"`

	// LogRetentionDays is common-envelope: one value applied to every
	// service's log group/workspace. Not validated here; the real
	// enum/range constraints are enforced by `terraform validate`.
	LogRetentionDays *int `yaml:"log_retention_days,omitempty"`

	// Domain is the custom domain a CDN-enabled service
	// (`x-cloud.cdn: true` in docker-compose.yml) is served under.
	// Required for GCP if any service declares `cdn: true`; optional
	// for AWS/Azure, which each get a free CloudFront/Front Door hostname.
	Domain *string `yaml:"domain,omitempty"`

	AWS   *AwsInitConfig   `yaml:"aws,omitempty"`
	Azure *AzureInitConfig `yaml:"azure,omitempty"`
	Gcp   *GcpInitConfig   `yaml:"gcp,omitempty"`

	// Backend is the optional remote Terraform state backend this
	// environment (and every app compiled against it) uses. Nil means
	// an ordinary local terraform.tfstate file.
	//
	// Like AWS/Azure/Gcp above, exactly one of Backend's own AWS/
	// Azure/Gcp fields may be set, and it must match Provider.
	Backend *BackendConfig `yaml:"backend,omitempty"`
}

// BackendConfig is the `backend:` block of an authored environment.yaml.
// The state key within each backend is never authored here -- it's
// always derived from Name.
type BackendConfig struct {
	AWS   *AwsBackendConfig   `yaml:"aws,omitempty"`
	Azure *AzureBackendConfig `yaml:"azure,omitempty"`
	Gcp   *GcpBackendConfig   `yaml:"gcp,omitempty"`
}

// AwsBackendConfig configures Terraform's `s3` backend. DynamoDBTable is
// optional but strongly recommended.
type AwsBackendConfig struct {
	Bucket        string `yaml:"bucket"`
	Region        string `yaml:"region"`
	DynamoDBTable string `yaml:"dynamodb_table,omitempty"`
}

// AzureBackendConfig configures Terraform's `azurerm` backend.
// UseAzureADAuth defaults to true; a *bool so an explicit `false` is
// distinguishable from "not set".
type AzureBackendConfig struct {
	ResourceGroupName  string `yaml:"resource_group_name"`
	StorageAccountName string `yaml:"storage_account_name"`
	ContainerName      string `yaml:"container_name"`
	UseAzureADAuth     *bool  `yaml:"use_azuread_auth,omitempty"`
}

// GcpBackendConfig configures Terraform's `gcs` backend. No lock-table
// equivalent field exists -- GCS backend locking is native.
type GcpBackendConfig struct {
	Bucket string `yaml:"bucket"`
}

// AwsInitConfig is the `aws:` block of an authored environment.yaml.
type AwsInitConfig struct {
	VpcCIDR        string  `yaml:"vpc_cidr,omitempty"`
	AzCount        *int    `yaml:"az_count,omitempty"`
	CreateALB      *bool   `yaml:"create_alb,omitempty"`
	CertificateArn *string `yaml:"certificate_arn,omitempty"`
	AwsEndpoint    *string `yaml:"aws_endpoint,omitempty"`
}

// AzureInitConfig is the `azure:` block. Named VnetCIDR rather than
// VpcCIDR since Azure calls the resource a VNet, not a VPC.
type AzureInitConfig struct {
	VnetCIDR string `yaml:"vnet_cidr,omitempty"`
}

// GcpInitConfig is the `gcp:` block. ProjectID is required: GCP
// inference (internal/compiler/gcp/infer.go) depends on it throughout.
type GcpInitConfig struct {
	VpcCIDR   string `yaml:"vpc_cidr,omitempty"`
	ProjectID string `yaml:"project_id"`
}
