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

	// backend.remote isn't a models.InitConfig field with a yaml tag
	// (see BackendConfig's own doc comment for why): its shape depends
	// on Provider, which yaml.Unmarshal has already resolved onto
	// config above, so it's decoded here into whichever of
	// config.Backend's own AWS/Azure/Gcp fields matches.
	if err := decodeBackendRemote(rawMap, &config); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if err := Validate(&config); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &config, nil
}

// remoteFieldsByProvider lists the fields backend.remote: accepts for
// each provider, kept explicit so a reviewer notices when a field is
// added -- mirrors knownTopLevelKeys' own reasoning.
var remoteFieldsByProvider = map[string]map[string]bool{
	"aws":   {"bucket": true, "region": true, "dynamodb_table": true},
	"azure": {"resource_group_name": true, "storage_account_name": true, "container_name": true, "use_azuread_auth": true},
	"gcp":   {"bucket": true},
}

// decodeBackendRemote decodes rawMap's backend.remote block (if
// present) into whichever of config.Backend's own AWS/Azure/Gcp fields
// matches config.Provider -- the YAML key never names the cloud a
// second time, since config.Provider already does (see
// models.BackendConfig's own doc comment).
func decodeBackendRemote(rawMap map[string]any, config *models.InitConfig) error {
	backendRaw, _ := rawMap["backend"].(map[string]any)
	if backendRaw == nil {
		return nil
	}

	var unknownBackendKeys []string
	for key := range backendRaw {
		if key != "local" && key != "remote" {
			unknownBackendKeys = append(unknownBackendKeys, key)
		}
	}
	if len(unknownBackendKeys) > 0 {
		sort.Strings(unknownBackendKeys)
		return fmt.Errorf("backend: unknown field(s): %v (expected local: or remote:)", unknownBackendKeys)
	}

	remoteRaw, ok := backendRaw["remote"]
	if !ok {
		return nil
	}
	remoteBlock, ok := remoteRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("backend.remote must be a mapping")
	}

	knownFields, providerSupported := remoteFieldsByProvider[config.Provider]
	if !providerSupported {
		// An unsupported provider is caught by Validate, called right
		// after Load's own decodeBackendRemote call, with a clearer
		// message than any unknown-field error this function could
		// give -- nothing further to check here.
		return nil
	}
	var unknown []string
	for key := range remoteBlock {
		if !knownFields[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("backend.remote: unknown field(s) for provider %q: %v", config.Provider, unknown)
	}

	remoteYAML, err := yaml.Marshal(remoteBlock)
	if err != nil {
		return fmt.Errorf("backend.remote: %w", err)
	}

	switch config.Provider {
	case "aws":
		var b models.AwsBackendConfig
		if err := yaml.Unmarshal(remoteYAML, &b); err != nil {
			return fmt.Errorf("backend.remote: %w", err)
		}
		config.Backend.AWS = &b
	case "azure":
		var b models.AzureBackendConfig
		if err := yaml.Unmarshal(remoteYAML, &b); err != nil {
			return fmt.Errorf("backend.remote: %w", err)
		}
		config.Backend.Azure = &b
	case "gcp":
		var b models.GcpBackendConfig
		if err := yaml.Unmarshal(remoteYAML, &b); err != nil {
			return fmt.Errorf("backend.remote: %w", err)
		}
		config.Backend.Gcp = &b
	}

	return nil
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

// validateBackend enforces the required fields each backend type
// needs. backend: itself is required (see models.BackendConfig's own
// doc comment for why) -- caught here by an explicit zero-value check,
// since a zero-value BackendConfig (every field nil) occurs whenever
// backend: is missing entirely, present but empty (e.g. `backend:`
// with nothing after it, or `backend: {}`), or `backend.remote` was
// given for an unsupported provider (Validate's own provider check
// runs after this and catches that case with a clearer message).
//
// Unlike aws:/azure:/gcp: at the top level, there's no
// "backend has a block not matching provider" case to check here:
// decodeBackendRemote already only ever populates the one field
// matching Provider, so a mismatch is structurally impossible by the
// time validateBackend runs.
func validateBackend(config *models.InitConfig) error {
	backend := config.Backend
	if backend.Local == nil && backend.AWS == nil && backend.Azure == nil && backend.Gcp == nil {
		return fmt.Errorf(`backend: is required -- use local:/remote: (see docs/environment-and-state.md)`)
	}

	if backend.Local != nil {
		if backend.AWS != nil || backend.Azure != nil || backend.Gcp != nil {
			return fmt.Errorf("backend has both local: and remote: blocks; only one is allowed")
		}
		if backend.Local.Path == "" {
			return fmt.Errorf("backend.local requires path")
		}
		return nil
	}

	switch config.Provider {
	case "aws":
		b := backend.AWS
		if b == nil {
			return fmt.Errorf(`declares provider "aws" but backend has neither local: nor remote:`)
		}
		if b.Bucket == "" || b.Region == "" {
			return fmt.Errorf("backend.remote requires bucket and region")
		}
	case "azure":
		b := backend.Azure
		if b == nil {
			return fmt.Errorf(`declares provider "azure" but backend has neither local: nor remote:`)
		}
		if b.ResourceGroupName == "" || b.StorageAccountName == "" || b.ContainerName == "" {
			return fmt.Errorf("backend.remote requires resource_group_name, storage_account_name, and container_name")
		}
	case "gcp":
		b := backend.Gcp
		if b == nil {
			return fmt.Errorf(`declares provider "gcp" but backend has neither local: nor remote:`)
		}
		if b.Bucket == "" {
			return fmt.Errorf("backend.remote requires bucket")
		}
	}

	return nil
}

// BackendWarnings returns human-readable, non-fatal warnings about
// config's backend:. The caller is responsible for printing these;
// this package only decides what they say. There is no "no backend
// configured" case: local state is always an authored choice
// (`backend: {local: {path: ...}}`), not an omission, so there's
// nothing to warn about beyond backend-specific weaknesses.
func BackendWarnings(config *models.InitConfig) []string {
	if config.Provider == "aws" && config.Backend.AWS != nil && config.Backend.AWS.DynamoDBTable == "" {
		return []string{
			"backend.aws has no dynamodb_table configured — concurrent `terraform apply`/`destroy` runs " +
				"against this environment are not protected by a state lock (see docs/environment-and-state.md).",
		}
	}

	return nil
}
