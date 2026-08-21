package shared

import "github.com/gecburton/cloudcompose/internal/models"

// AppBackendBlock returns the `terraform.backend` block value an app's
// main.tf.json should emit, given the backend config its environment was
// generated with and this app's own project name -- or nil if backend is
// nil (the environment has no backend configured).
//
// Keyed by BackendKeyForApp instead of BackendKeyForEnvironment: an app's
// state must live in the same bucket/storage account as its environment's,
// under its own, app-specific key, never the environment's own key.
func AppBackendBlock(envName, projectName string, backend *models.BackendConfig) map[string]any {
	if backend == nil {
		return nil
	}

	switch {
	case backend.AWS != nil:
		s3Backend := map[string]any{
			"bucket":  backend.AWS.Bucket,
			"key":     BackendKeyForApp(envName, projectName),
			"region":  backend.AWS.Region,
			"encrypt": true,
		}
		if backend.AWS.DynamoDBTable != "" {
			s3Backend["dynamodb_table"] = backend.AWS.DynamoDBTable
		}
		return map[string]any{"s3": s3Backend}

	case backend.Azure != nil:
		azurermBackend := map[string]any{
			"resource_group_name":  backend.Azure.ResourceGroupName,
			"storage_account_name": backend.Azure.StorageAccountName,
			"container_name":       backend.Azure.ContainerName,
			"key":                  BackendKeyForApp(envName, projectName),
		}
		useAzureADAuth := true
		if backend.Azure.UseAzureADAuth != nil {
			useAzureADAuth = *backend.Azure.UseAzureADAuth
		}
		azurermBackend["use_azuread_auth"] = useAzureADAuth
		return map[string]any{"azurerm": azurermBackend}

	case backend.Gcp != nil:
		// Terraform's gcs backend uses "prefix", not "key".
		return map[string]any{
			"gcs": map[string]any{
				"bucket": backend.Gcp.Bucket,
				"prefix": BackendKeyForApp(envName, projectName),
			},
		}

	default:
		return nil
	}
}
