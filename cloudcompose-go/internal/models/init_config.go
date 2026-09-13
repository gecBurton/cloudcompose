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
	// (state stays on this machine, at an authored path) or `remote:`
	// (a real Terraform backend, shaped by Provider -- see
	// BackendConfig's own doc comment for why there's no separate
	// aws:/azure:/gcp: key under backend: the way there is at the top
	// level). There is no way to omit backend: entirely: unlike every
	// other field here, an omitted backend: used to silently mean
	// local state, which is exactly the kind of implicit default this
	// project's identity model rules out elsewhere -- see
	// docs/deployment-identity-design.md item 4. Populated by
	// initconfig.Load, which also enforces that exactly one of
	// Backend's own Local/AWS/Azure/Gcp fields is set.
	Backend BackendConfig `yaml:"backend"`
}

// BackendConfig is the `backend:` block of an authored environment.yaml:
// either `local:` (state stays on this machine) or `remote:` (a real
// Terraform backend). Unlike Local, Remote's own required fields
// aren't self-describing from the YAML key alone -- they depend on
// which cloud InitConfig.Provider names (S3 needs bucket/region;
// azurerm needs storage_account_name/container_name; GCS needs just
// bucket), so initconfig.Load decodes Remote into whichever of
// AWS/Azure/Gcp matches Provider, rather than the YAML key itself
// naming the cloud a second time (Provider already does that).
//
// A zero-value BackendConfig (every field nil) is not a valid parsed
// value; initconfig.Load always produces exactly one field set,
// enforced by initconfig.Validate.
type BackendConfig struct {
	Local *LocalBackendConfig `yaml:"local,omitempty"`

	// Remote is decoded by initconfig.Load, not by BackendConfig's own
	// (nonexistent) UnmarshalYAML: exactly one of AWS/Azure/Gcp is set,
	// chosen by InitConfig.Provider, mirroring how DecodeBackendOutput
	// already decodes the *deployed-facts* side of a backend (a
	// `provider` tag alongside a same-named block) -- see
	// internal/compiler/shared/backend_output_decode.go.
	AWS   *AwsBackendConfig   `yaml:"-"`
	Azure *AzureBackendConfig `yaml:"-"`
	Gcp   *GcpBackendConfig   `yaml:"-"`
}

// MarshalYAML renders whichever of Local/AWS/Azure/Gcp is set back
// into `{local: {...}}` or `{remote: {...}}` -- the inverse of
// initconfig.Load's own decodeBackendRemote, needed since AWS/Azure/Gcp
// are tagged `yaml:"-"` (their YAML key is `remote`, not their own
// field name, so the default marshaller can't place them). Used when
// env_init.go writes a resolved copy of environment.yaml back out.
func (b BackendConfig) MarshalYAML() (interface{}, error) {
	if b.Local != nil {
		return struct {
			Local *LocalBackendConfig `yaml:"local"`
		}{b.Local}, nil
	}
	switch {
	case b.AWS != nil:
		return struct {
			Remote *AwsBackendConfig `yaml:"remote"`
		}{b.AWS}, nil
	case b.Azure != nil:
		return struct {
			Remote *AzureBackendConfig `yaml:"remote"`
		}{b.Azure}, nil
	case b.Gcp != nil:
		return struct {
			Remote *GcpBackendConfig `yaml:"remote"`
		}{b.Gcp}, nil
	}
	return struct{}{}, nil
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

// AwsBackendConfig is `backend.remote:`'s shape when provider: aws.
// DynamoDBTable is optional but strongly recommended.
type AwsBackendConfig struct {
	Bucket        string `yaml:"bucket"`
	Region        string `yaml:"region"`
	DynamoDBTable string `yaml:"dynamodb_table,omitempty"`
}

// AzureBackendConfig is `backend.remote:`'s shape when provider: azure.
// UseAzureADAuth defaults to true; a *bool so an explicit `false` is
// distinguishable from "not set".
type AzureBackendConfig struct {
	ResourceGroupName  string `yaml:"resource_group_name"`
	StorageAccountName string `yaml:"storage_account_name"`
	ContainerName      string `yaml:"container_name"`
	UseAzureADAuth     *bool  `yaml:"use_azuread_auth,omitempty"`
}

// GcpBackendConfig is `backend.remote:`'s shape when provider: gcp. No
// lock-table equivalent field exists -- GCS backend locking is native.
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
