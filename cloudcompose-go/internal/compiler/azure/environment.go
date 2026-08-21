package azure

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// LoadAzureEnvironment resolves an Azure environment's facts by running
// `terraform output -json` in dir and decoding its `environment` output
// into models.AzureEnvironment, along with the optional `backend` output
// into env.Backend.
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

	backendRaw, err := shared.OptionalTerraformOutputs(dir, "backend")
	if err != nil {
		return nil, err
	}
	env.Backend = shared.DecodeBackendOutput(backendRaw)

	// AzureEnvironment carries no cross-field validation.

	return &env, nil
}
