// Package initconfig loads and validates the authored `environment.yaml`
// input to `composey init` -- the decisions a human makes about a shared
// environment (region, VPC CIDR, whether to create a load balancer, a
// GCP project ID) as opposed to the facts Terraform assigns once that
// infrastructure exists (a VPC ID, an ALB ARN), which `composey main`
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

	"github.com/gecburton/composey/internal/models"
	yaml "go.yaml.in/yaml/v4"
)

var supportedProviders = map[string]bool{"aws": true, "azure": true, "gcp": true}

// knownTopLevelKeys mirrors models.InitConfig's own yaml tags. Kept as an
// explicit list (rather than reflecting over the struct) for the same
// reason models/compose.go's XComposey.UnmarshalJSON hand-lists its own
// known keys: an explicit list is what actually gets reviewed when a
// field is added, whereas reflection would silently stay "correct" even
// if someone forgot to update a parallel human-facing error message.
var knownTopLevelKeys = map[string]bool{
	"provider": true, "name": true, "region": true, "tags": true,
	"retain_data_on_destroy": true, "domain": true,
	"aws": true, "azure": true, "gcp": true,
}

// Load reads and validates an environment.yaml file at path. Returns
// (nil, nil) if the file does not exist -- not an error, since
// `composey init` falls back to flags-only when no authored file is
// present (see docs/authored-environment-config.md's "composey init
// behavior", step 3).
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

	return nil
}
