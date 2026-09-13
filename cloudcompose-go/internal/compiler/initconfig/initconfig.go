// Package initconfig loads and validates the authored `environment.yaml`
// input to `cloudcompose init`.
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

// knownTopLevelKeys mirrors models.InitConfig's yaml tags, kept as an
// explicit list so a reviewer notices when a field is added.
var knownTopLevelKeys = map[string]bool{
	"provider": true, "name": true, "region": true, "tags": true,
	"retain_data_on_destroy": true, "domain": true,
	"high_availability_enabled": true, "backup_retention_days": true,
	"log_retention_days": true,
	"aws":                true, "azure": true, "gcp": true,
	"backend": true,
}

// Load reads and validates an environment.yaml file at path. Returns
// (nil, nil) if the file does not exist, since deciding what to do
// about a missing file is a CLI-level concern.
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
// one of aws/azure/gcp, exactly the matching provider block is present,
// and GCP's project_id is present since inference depends on it.
func Validate(config *models.InitConfig) error {
	if config.Name == "" {
		return fmt.Errorf("name is required")
	}
	// name is also cloudcompose init's output directory name and part of
	// the backend key, so it can't contain "/".
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
// backend: as Validate applies to aws:/azure:/gcp:, plus the required
// fields each backend type needs. backend: itself is required (see
// models.BackendConfig's own doc comment for why) -- caught upstream by
// yaml.Unmarshal already requiring some value for the field, so a
// zero-value BackendConfig here (IsLocal false, every provider block
// nil) only occurs if backend: was present but empty (e.g. `backend:`
// with nothing after it, or `backend: {}`), which is rejected outright
// rather than silently treated as `local`.
func validateBackend(config *models.InitConfig) error {
	backend := config.Backend
	if !backend.IsLocal && backend.AWS == nil && backend.Azure == nil && backend.Gcp == nil {
		return fmt.Errorf(`backend: is required -- use "local" or a mapping with aws:/azure:/gcp: (see docs/authored-environment-config.md)`)
	}
	if backend.IsLocal {
		return nil
	}

	backendPresent := map[string]bool{
		"aws":   backend.AWS != nil,
		"azure": backend.Azure != nil,
		"gcp":   backend.Gcp != nil,
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
		b := backend.AWS
		if b == nil {
			return fmt.Errorf(`declares provider "aws" but backend: has no aws: block (and is not "local")`)
		}
		if b.Bucket == "" || b.Region == "" {
			return fmt.Errorf("backend.aws requires bucket and region")
		}
	case "azure":
		b := backend.Azure
		if b == nil {
			return fmt.Errorf(`declares provider "azure" but backend: has no azure: block (and is not "local")`)
		}
		if b.ResourceGroupName == "" || b.StorageAccountName == "" || b.ContainerName == "" {
			return fmt.Errorf("backend.azure requires resource_group_name, storage_account_name, and container_name")
		}
	case "gcp":
		b := backend.Gcp
		if b == nil {
			return fmt.Errorf(`declares provider "gcp" but backend: has no gcp: block (and is not "local")`)
		}
		if b.Bucket == "" {
			return fmt.Errorf("backend.gcp requires bucket")
		}
	}

	return nil
}

// BackendWarnings returns human-readable, non-fatal warnings about
// config's backend:. The caller is responsible for printing these;
// this package only decides what they say. Unlike before backend: was
// required, there is no "no backend configured" case: local state is
// now always an authored choice (`backend: local`), not an omission,
// so there's nothing to warn about beyond backend-specific weaknesses.
func BackendWarnings(config *models.InitConfig) []string {
	if config.Provider == "aws" && config.Backend.AWS != nil && config.Backend.AWS.DynamoDBTable == "" {
		return []string{
			"backend.aws has no dynamodb_table configured — concurrent `terraform apply`/`destroy` runs " +
				"against this environment are not protected by a state lock (see docs/multi-user-state.md).",
		}
	}

	return nil
}
