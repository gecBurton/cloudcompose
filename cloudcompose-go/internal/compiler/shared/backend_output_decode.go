package shared

import "github.com/gecburton/cloudcompose/internal/models"

// DecodeBackendOutput decodes the shape every cloud's own
// Generate*Environment writes into its `output "backend"` block
// (`{"provider": "aws"|"azure"|"gcp", "aws"|"azure"|"gcp": {...}}` --
// see internal/compiler/{aws,azure,gcp}/environment_generator.go) back
// into a *models.BackendConfig, or nil if raw is nil (the environment
// was generated without backend: configured -- see
// OptionalTerraformOutputs's own doc comment for why that's not itself
// an error).
//
// Deliberately does not decode a "key"/"prefix" field, because none is
// ever written there in the first place: the environment's own key is
// specific to the environment, and must never be read back and reused
// verbatim by an app -- every app derives its own key via
// shared.BackendKeyForApp instead. See docs/multi-user-state.md.
func DecodeBackendOutput(raw map[string]any) *models.BackendConfig {
	if raw == nil {
		return nil
	}

	provider, _ := raw["provider"].(string)
	backend := &models.BackendConfig{}

	switch provider {
	case "aws":
		block, _ := raw["aws"].(map[string]any)
		if block == nil {
			return nil
		}
		bucket, _ := block["bucket"].(string)
		region, _ := block["region"].(string)
		backend.AWS = &models.AwsBackendConfig{
			Bucket:        bucket,
			Region:        region,
			DynamoDBTable: valueOrEmptyString(block["dynamodb_table"]),
		}
	case "azure":
		block, _ := raw["azure"].(map[string]any)
		if block == nil {
			return nil
		}
		resourceGroupName, _ := block["resource_group_name"].(string)
		storageAccountName, _ := block["storage_account_name"].(string)
		containerName, _ := block["container_name"].(string)
		azureBackend := &models.AzureBackendConfig{
			ResourceGroupName:  resourceGroupName,
			StorageAccountName: storageAccountName,
			ContainerName:      containerName,
		}
		if v, ok := block["use_azuread_auth"].(bool); ok {
			azureBackend.UseAzureADAuth = &v
		}
		backend.Azure = azureBackend
	case "gcp":
		block, _ := raw["gcp"].(map[string]any)
		if block == nil {
			return nil
		}
		bucket, _ := block["bucket"].(string)
		backend.Gcp = &models.GcpBackendConfig{Bucket: bucket}
	default:
		return nil
	}

	return backend
}

// valueOrEmptyString type-asserts v as a string, returning "" for nil
// or any other type -- used for optional string fields decoded out of
// a Terraform output's map[string]any shape (dynamodb_table is the only
// current caller: absent when no lock table was configured).
func valueOrEmptyString(v any) string {
	s, _ := v.(string)
	return s
}
