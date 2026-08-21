package shared

import "github.com/gecburton/cloudcompose/internal/models"

// DecodeBackendOutput decodes the shape every cloud's Generate*Environment
// writes into its `output "backend"` block
// (`{"provider": "aws"|"azure"|"gcp", "aws"|"azure"|"gcp": {...}}`) back
// into a *models.BackendConfig, or nil if raw is nil.
//
// Deliberately does not decode a "key"/"prefix" field: the environment's
// own key must never be read back and reused verbatim by an app -- every
// app derives its own key via BackendKeyForApp instead.
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
// or any other type.
func valueOrEmptyString(v any) string {
	s, _ := v.(string)
	return s
}
