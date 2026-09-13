package models

import (
	"fmt"

	yaml "go.yaml.in/yaml/v4"
)

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
	// (and every app compiled against it) uses -- `local` (state stays
	// on this machine) or a real remote backend (`aws:`/`azure:`/
	// `gcp:`). There is no way to omit backend: entirely: unlike every
	// other field here, an omitted backend: used to silently mean
	// local state, which is exactly the kind of implicit default this
	// project's identity model rules out elsewhere -- see
	// docs/deployment-identity-design.md item 4. Validated by
	// initconfig.Validate, which also enforces that at most one of
	// Backend's own AWS/Azure/Gcp fields is set, and that it matches
	// Provider.
	Backend BackendConfig `yaml:"backend"`
}

// BackendConfig is the `backend:` block of an authored environment.yaml:
// either the bare string `local` (state stays on this machine, no
// `terraform.backend` block emitted), or a mapping naming exactly one
// of aws/azure/gcp. The state key within a remote backend is never
// authored here -- it's always derived from InitConfig.Name.
//
// IsLocal distinguishes "authored as `local`" from "zero value/not yet
// unmarshalled" -- a BackendConfig with IsLocal false and every
// AWS/Azure/Gcp field nil is not a valid parsed value; initconfig.Load
// always produces either IsLocal true or exactly one of AWS/Azure/Gcp
// set, enforced by UnmarshalYAML and initconfig.Validate together.
type BackendConfig struct {
	IsLocal bool
	AWS     *AwsBackendConfig
	Azure   *AzureBackendConfig
	Gcp     *GcpBackendConfig
}

// UnmarshalYAML accepts either the bare scalar `local` or a mapping
// with exactly one of aws/azure/gcp -- anything else (an empty mapping,
// a list, a number, the string "aws" with no block, etc.) is rejected
// outright rather than silently treated as one or the other.
func (b *BackendConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		if s != "local" {
			return fmt.Errorf(`backend: %q is not a supported value -- use "local" or a mapping with aws:/azure:/gcp:`, s)
		}
		*b = BackendConfig{IsLocal: true}
		return nil
	}

	var raw struct {
		AWS   *AwsBackendConfig   `yaml:"aws"`
		Azure *AzureBackendConfig `yaml:"azure"`
		Gcp   *GcpBackendConfig   `yaml:"gcp"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*b = BackendConfig{AWS: raw.AWS, Azure: raw.Azure, Gcp: raw.Gcp}
	return nil
}

// MarshalYAML renders IsLocal as the bare scalar "local", or a mapping
// with whichever of AWS/Azure/Gcp is set -- the inverse of
// UnmarshalYAML, used when env_init.go writes a resolved copy of
// environment.yaml back out.
func (b BackendConfig) MarshalYAML() (interface{}, error) {
	if b.IsLocal {
		return "local", nil
	}
	return struct {
		AWS   *AwsBackendConfig   `yaml:"aws,omitempty"`
		Azure *AzureBackendConfig `yaml:"azure,omitempty"`
		Gcp   *GcpBackendConfig   `yaml:"gcp,omitempty"`
	}{b.AWS, b.Azure, b.Gcp}, nil
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
