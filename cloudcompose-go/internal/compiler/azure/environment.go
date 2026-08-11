package azure

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// LoadAzureEnvironment resolves an Azure environment's facts by running
// `terraform output -json` in dir and decoding its `environment` output
// into models.AzureEnvironment. See aws.LoadAwsEnvironment's own doc
// comment for why this reads Terraform's own live state rather than a
// generated file.
func LoadAzureEnvironment(dir string) (*models.AzureEnvironment, error) {
	raw, err := shared.TerraformOutputs(dir, "environment")
	if err != nil {
		return nil, err
	}

	target, _ := raw["target"].(string)
	if target == "" {
		target = "azure"
	}
	if target != "azure" {
		return nil, fmt.Errorf(
			"%s declares target %q; this loader only supports \"azure\"",
			dir, target,
		)
	}

	env := models.NewAzureEnvironment()
	env.Name, _ = raw["name"].(string)
	if region, ok := raw["region"].(string); ok && region != "" {
		env.Region = region
	}
	if logRetentionDays, ok := raw["log_retention_days"].(float64); ok {
		env.LogRetentionDays = int(logRetentionDays)
	}
	if retainData, ok := raw["retain_data_on_destroy"].(bool); ok {
		env.RetainDataOnDestroy = retainData
	}
	if highAvailability, ok := raw["high_availability_enabled"].(bool); ok {
		env.HighAvailabilityEnabled = highAvailability
	}
	if backupRetentionDays, ok := raw["backup_retention_days"].(float64); ok {
		env.BackupRetentionDays = int(backupRetentionDays)
	}
	env.Tags = shared.ToStringMap(raw["tags"])
	env.LogAnalyticsWorkspaceID, _ = raw["log_analytics_workspace_id"].(string)
	env.ResourceGroupName, _ = raw["resource_group_name"].(string)
	env.VnetID, _ = raw["vnet_id"].(string)
	env.VnetName, _ = raw["vnet_name"].(string)
	env.AppsCIDR, _ = raw["apps_cidr"].(string)
	env.ContainerRegistryName = shared.ToStringPtr(raw["container_registry_name"])
	env.PostgresqlServerID = shared.ToStringPtr(raw["postgresql_server_id"])
	env.UserAssignedIdentityID = shared.ToStringPtr(raw["user_assigned_identity_id"])
	env.AzureEndpoint = shared.ToStringPtr(raw["azure_endpoint"])

	// AzureEnvironment carries no cross-field validation (no equivalent to
	// AwsEnvironment's ALB check).

	return &env, nil
}
