package models

// InitConfig is the authored input to `cloudcompose init`: the decisions a
// human makes about a shared environment before any infrastructure
// exists (region, VPC CIDR, whether to create a load balancer, a GCP
// project ID, etc.), as opposed to the *facts* Terraform assigns once
// that infrastructure is created (a VPC ID, an ALB ARN) -- those live in
// the generated Aws/Azure/GcpEnvironment models in environment.go and
// never appear here.
//
// See docs/authored-environment-config.md for the full design rationale:
// docker-compose.yml is authored, versioned, reviewable source; the
// environment side of cloudcompose had no equivalent, only ephemeral CLI
// flags. InitConfig is that missing source file, conventionally named
// environment.yaml (distinct from the generated environment.facts.json).
//
// Exactly one of AWS/Azure/Gcp may be set, and it must match Provider --
// enforced by the loader (internal/compiler/initconfig), not by this
// struct itself, since Go has no sum-type/discriminated-union construct
// to enforce it structurally.
type InitConfig struct {
	Provider            string            `yaml:"provider"`
	Name                string            `yaml:"name"`
	Region              string            `yaml:"region,omitempty"`
	Tags                map[string]string `yaml:"tags,omitempty"`
	RetainDataOnDestroy *bool             `yaml:"retain_data_on_destroy,omitempty"`

	// HighAvailabilityEnabled/BackupRetentionDays are common-envelope,
	// not per-provider, the same as RetainDataOnDestroy above: applied
	// uniformly to every database this environment's apps create,
	// regardless of which cloud they target (AWS's aws_db_instance.multi_az/
	// backup_retention_period, Azure's azurerm_*_flexible_server's
	// high_availability/backup_retention_days) -- see
	// docs/azure-aws-parity-todo.md's Priority 4 backup/HA item. Not yet
	// wired for GCP (Cloud SQL has its own equivalent settings), left
	// for a follow-up rather than blocking this on all three clouds at
	// once.
	HighAvailabilityEnabled *bool `yaml:"high_availability_enabled,omitempty"`
	BackupRetentionDays     *int  `yaml:"backup_retention_days,omitempty"`

	// Domain is the custom domain a CDN-enabled service
	// (`x-cloud.cdn: true` in docker-compose.yml) is served under.
	// Common-envelope, not per-cloud: a domain is owned once, by the
	// environment/account, not per compose file -- the same reasoning
	// that put `region`/`tags` here rather than duplicating them per
	// provider block. Required for GCP if any service declares
	// `cdn: true` (a Google-managed certificate cannot be issued without
	// one -- see docs/spikes/gcp/README.md's "cdn: true is not
	// self-sufficient on GCP"); optional for AWS/Azure, which each get a
	// free CloudFront/Front Door hostname without one.
	Domain *string `yaml:"domain,omitempty"`

	AWS   *AwsInitConfig   `yaml:"aws,omitempty"`
	Azure *AzureInitConfig `yaml:"azure,omitempty"`
	Gcp   *GcpInitConfig   `yaml:"gcp,omitempty"`
}

// AwsInitConfig is the `aws:` block of an authored environment.yaml,
// mirroring GenerateAwsEnvironment's own decision parameters exactly
// (internal/compiler/aws/environment_generator.go) -- everything here is
// something a human can and must decide before any AWS resource exists.
type AwsInitConfig struct {
	VpcCIDR        string  `yaml:"vpc_cidr,omitempty"`
	AzCount        *int    `yaml:"az_count,omitempty"`
	CreateALB      *bool   `yaml:"create_alb,omitempty"`
	CertificateArn *string `yaml:"certificate_arn,omitempty"`
	AwsEndpoint    *string `yaml:"aws_endpoint,omitempty"`
}

// AzureInitConfig is the `azure:` block. Named VnetCIDR rather than
// VpcCIDR -- Azure calls the resource a VNet, not a VPC; the old
// `--vpc-cidr` flag applied AWS terminology uniformly across all three
// clouds, which this schema is a deliberate chance to stop doing.
type AzureInitConfig struct {
	VnetCIDR string `yaml:"vnet_cidr,omitempty"`
}

// GcpInitConfig is the `gcp:` block. ProjectID is required: GCP
// inference (internal/compiler/gcp/infer.go) depends on env.ProjectID
// throughout, but nothing before this schema ever asked for it as an
// `init`-time decision -- cloudcompose init --provider gcp generated an
// environment.facts.json with no project_id anywhere in it, discovered
// only when cloudcompose main later failed against the incomplete file. See
// docs/authored-environment-config.md's "The project_id gap".
type GcpInitConfig struct {
	VpcCIDR   string `yaml:"vpc_cidr,omitempty"`
	ProjectID string `yaml:"project_id"`
}
