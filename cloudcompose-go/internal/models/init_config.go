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

	// Backend is the required Terraform state backend this environment
	// (and every app compiled against it) uses -- a `local:` block
	// (state stays on this machine, at an authored path) or a real
	// remote backend (`aws:`/`azure:`/`gcp:`). There is no way to omit
	// backend: entirely: unlike every other field here, an omitted
	// backend: used to silently mean local state, which is exactly the
	// kind of implicit default this project's identity model rules out
	// elsewhere -- see docs/deployment-identity-design.md item 4.
	// Validated by initconfig.Validate, which also enforces that at
	// most one of Backend's own Local/AWS/Azure/Gcp fields is set, and
	// that a remote one matches Provider.
	Backend BackendConfig `yaml:"backend"`
}

// BackendConfig is the `backend:` block of an authored environment.yaml:
// a mapping naming exactly one of local/aws/azure/gcp. The state key
// within a remote backend is never authored here -- it's always
// derived from InitConfig.Name.
//
// A zero-value BackendConfig (every field nil) is not a valid parsed
// value; initconfig.Load always produces exactly one field set,
// enforced by initconfig.Validate.
type BackendConfig struct {
	Local *LocalBackendConfig `yaml:"local,omitempty"`
	AWS   *AwsBackendConfig   `yaml:"aws,omitempty"`
	Azure *AzureBackendConfig `yaml:"azure,omitempty"`
	Gcp   *GcpBackendConfig   `yaml:"gcp,omitempty"`
}

// LocalBackendConfig configures Terraform's `local` backend.
//
// Path is required, with no default: an environment's state file
// location is otherwise an ambient side effect of whichever directory
// `terraform apply` happened to be run in, not an authored fact --
// exactly the kind of implicit default this project's identity model
// rules out elsewhere. Resolved relative to environment.yaml's own
// directory (matching how init/compile derive every other output
// location from an input file's location, never the shell's current
// directory), never as an absolute path or relative to cwd.
type LocalBackendConfig struct {
	Path string `yaml:"path"`
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
