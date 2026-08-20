// Package initconfig loads and validates the authored `environment.yaml`
// input to `cloudcompose init` -- the decisions a human makes about a shared
// environment (region, VPC CIDR, whether to create a load balancer, a
// GCP project ID) as opposed to the facts Terraform assigns once that
// infrastructure exists (a VPC ID, an ALB ARN), which `cloudcompose main`
// reads directly via `terraform output -json` instead (see
// internal/compiler/shared/terraform_outputs.go).
//
// See docs/authored-environment-config.md for the design this
// implements.
package initconfig

import (
	"fmt"
	"os"
	"sort"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
	yaml "go.yaml.in/yaml/v4"
)

var supportedProviders = map[string]bool{"aws": true, "azure": true, "gcp": true}

// knownTopLevelKeys mirrors models.InitConfig's own yaml tags. Kept as an
// explicit list (rather than reflecting over the struct) for the same
// reason models/compose.go's XCloud.UnmarshalJSON hand-lists its own
// known keys: an explicit list is what actually gets reviewed when a
// field is added, whereas reflection would silently stay "correct" even
// if someone forgot to update a parallel human-facing error message.
var knownTopLevelKeys = map[string]bool{
	"provider": true, "name": true, "region": true, "tags": true,
	"retain_data_on_destroy": true, "domain": true,
	"high_availability_enabled": true, "backup_retention_days": true,
	"log_retention_days": true,
	"aws":                true, "azure": true, "gcp": true,
	"backend": true,
}

// Load reads and validates an environment.yaml file at path. Returns
// (nil, nil) if the file does not exist -- not an error at this layer,
// since what to do about a missing file is a CLI-level concern
// (cmd/cloudcompose/init.go prints a specific "create one, here's how"
// message rather than a generic error). There is no flags-only fallback
// anymore: environment.yaml is cloudcompose init's only input (see
// docs/authored-environment-config.md).
func Load(path string) (*models.InitConfig, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var rawMap map[string]any
	if err := yaml.Unmarshal(raw, &rawMap); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var unknown []string
	for key := range rawMap {
		if !knownTopLevelKeys[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("%s: unknown field(s): %v", path, unknown)
	}

	var config models.InitConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := Validate(&config); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &config, nil
}

// Validate enforces the rules the YAML shape alone can't: provider is
// one of aws/azure/gcp, exactly the matching provider block is present
// (strict/discriminated -- an azure: block in a file declaring
// `provider: aws` is a mistake worth failing on, not silently ignoring;
// see docs/authored-environment-config.md's rationale, which points
// specifically at the old --azure-endpoint/--gcp-endpoint dead-flag
// problem this is meant to prevent a schema-level equivalent of), and
// GCP's project_id is present since inference depends on it throughout
// (see "The project_id gap" in the same doc).
func Validate(config *models.InitConfig) error {
	if config.Name == "" {
		return fmt.Errorf("name is required")
	}
	// Enforced here, not just structurally by BackendKeyForEnvironment's
	// own "apps/" nesting -- see shared.ValidateBackendName's own doc
	// comment for the concrete collision this closes (a name containing
	// "/" can otherwise make one environment's own backend key collide
	// with a completely different environment's app). This applies
	// regardless of whether backend: is even configured, since name is
	// also cloudcompose init's own env-<name> output directory name --
	// consistent with resolveProjectName's identical check in
	// cmd/cloudcompose/compile.go for --project.
	if err := shared.ValidateBackendName("name", config.Name); err != nil {
		return err
	}
	if !supportedProviders[config.Provider] {
		return fmt.Errorf("provider %q is not supported; supported: aws, azure, gcp", config.Provider)
	}

	present := map[string]bool{
		"aws":   config.AWS != nil,
		"azure": config.Azure != nil,
		"gcp":   config.Gcp != nil,
	}
	for provider, isPresent := range present {
		if provider != config.Provider && isPresent {
			return fmt.Errorf(
				"declares provider %q but also has a %q block; only the block matching provider is allowed",
				config.Provider, provider,
			)
		}
	}

	if config.Provider == "gcp" {
		if config.Gcp == nil || config.Gcp.ProjectID == "" {
			return fmt.Errorf("gcp.project_id is required")
		}
	}

	if err := validateBackend(config); err != nil {
		return err
	}

	return nil
}

// validateBackend enforces the same strict/discriminated rule on
// backend: that Validate already applies to the top-level aws:/azure:/
// gcp: blocks -- exactly the block matching provider may be set -- plus
// the required fields each backend type needs to be usable at all
// (Terraform itself would reject a backend block missing these, but
// failing here gives a clearer, environment.yaml-specific error instead
// of a `terraform init` failure two commands later). backend: itself
// being absent entirely is not an error -- see BackendWarnings for the
// non-fatal warning that covers that case instead.
func validateBackend(config *models.InitConfig) error {
	if config.Backend == nil {
		return nil
	}

	backendPresent := map[string]bool{
		"aws":   config.Backend.AWS != nil,
		"azure": config.Backend.Azure != nil,
		"gcp":   config.Backend.Gcp != nil,
	}
	for provider, isPresent := range backendPresent {
		if provider != config.Provider && isPresent {
			return fmt.Errorf(
				"declares provider %q but backend has a %q block; only the block matching provider is allowed",
				config.Provider, provider,
			)
		}
	}

	switch config.Provider {
	case "aws":
		b := config.Backend.AWS
		if b == nil {
			return nil
		}
		if b.Bucket == "" || b.Region == "" {
			return fmt.Errorf("backend.aws requires bucket and region")
		}
	case "azure":
		b := config.Backend.Azure
		if b == nil {
			return nil
		}
		if b.ResourceGroupName == "" || b.StorageAccountName == "" || b.ContainerName == "" {
			return fmt.Errorf("backend.azure requires resource_group_name, storage_account_name, and container_name")
		}
	case "gcp":
		b := config.Backend.Gcp
		if b == nil {
			return nil
		}
		if b.Bucket == "" {
			return fmt.Errorf("backend.gcp requires bucket")
		}
	}

	return nil
}

// BackendWarnings returns human-readable, non-fatal warnings about
// config's backend: (or lack of one) -- multi-user state sharing isn't
// safe without a remote backend, and AWS's S3 backend isn't
// concurrency-safe without a lock table, but neither is invalid
// config, so neither belongs in Validate's hard-error path. The caller
// (cmd/cloudcompose/init.go) is responsible for printing these to
// stderr; this package only decides what they say. See
// docs/multi-user-state.md.
func BackendWarnings(config *models.InitConfig) []string {
	if config.Backend == nil {
		return []string{
			"no backend configured — state is local to this machine. " +
				"Multiple users sharing this environment must configure `backend:` in environment.yaml (see docs/multi-user-state.md).",
		}
	}

	if config.Provider == "aws" && config.Backend.AWS != nil && config.Backend.AWS.DynamoDBTable == "" {
		return []string{
			"backend.aws has no dynamodb_table configured — concurrent `terraform apply`/`destroy` runs " +
				"against this environment are not protected by a state lock (see docs/multi-user-state.md).",
		}
	}

	return nil
}
