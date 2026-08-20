package shared

import "github.com/gecburton/cloudcompose/internal/models"

// AppBackendBlock returns the `terraform.backend` block value an app's
// own main.tf.json should emit, given the backend config its
// environment was generated with (env.Backend, decoded by
// DecodeBackendOutput) and this app's own project name -- or nil if
// backend is nil (the environment has no backend configured; see
// docs/multi-user-state.md's "no backend configured" default).
//
// Mirrors each environment_generator.go's own inline backend-block
// construction exactly (same s3/azurerm/gcs field names), but keyed by
// BackendKeyForApp instead of BackendKeyForEnvironment: an app's state
// must live in the same bucket/storage account as its environment's,
// under its own, app-specific key, never the environment's own key
// (every environment_generator.go's own `output "backend"` block
// deliberately omits the environment's key for exactly this reason --
// see e.g. aws/environment_generator.go's own doc comment).
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
		// Terraform's own gcs backend uses "prefix", not "key" -- see
		// gcp/environment_generator.go's own doc comment on the same
		// distinction.
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
