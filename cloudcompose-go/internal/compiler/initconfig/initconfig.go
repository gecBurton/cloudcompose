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
// fields each backend type needs. backend: being absent is not an
// error; see BackendWarnings.
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
// config's backend: (or lack of one). The caller is responsible for
// printing these; this package only decides what they say.
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
