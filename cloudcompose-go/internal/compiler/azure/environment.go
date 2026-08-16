package azure

import (
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
	if err := shared.RequireTarget(raw, dir, "azure"); err != nil {
		return nil, err
	}

	env := models.NewAzureEnvironment()
	common := shared.DecodeCommonEnvelope(raw)
	env.Name = common.Name
	if common.Region != nil {
		env.Region = *common.Region
	}
	if common.LogRetentionDays != nil {
		env.LogRetentionDays = *common.LogRetentionDays
	}
	if common.RetainDataOnDestroy != nil {
		env.RetainDataOnDestroy = *common.RetainDataOnDestroy
	}
	if common.HighAvailabilityEnabled != nil {
		env.HighAvailabilityEnabled = *common.HighAvailabilityEnabled
	}
	if common.BackupRetentionDays != nil {
		env.BackupRetentionDays = *common.BackupRetentionDays
	}
	env.Tags = common.Tags

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
