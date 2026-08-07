package azure

import (
	"fmt"
	"os"

	"github.com/gecburton/composey/internal/models"
	yaml "go.yaml.in/yaml/v4"
)

// azureEnvironmentYAML mirrors the on-disk shape of an Azure environment
// file, matching the field names AzureEnvironment declares in
// environment.py (snake_case, since these are hand-written YAML files,
// not Go/JSON values).
type azureEnvironmentYAML struct {
	Target              string            `yaml:"target"`
	Name                string            `yaml:"name"`
	Region              string            `yaml:"region"`
	LogRetentionDays    *int              `yaml:"log_retention_days"`
	RetainDataOnDestroy *bool             `yaml:"retain_data_on_destroy"`
	Tags                map[string]string `yaml:"tags"`

	ContainerAppsEnvironmentName string  `yaml:"container_apps_environment_name"`
	LogAnalyticsWorkspaceID      string  `yaml:"log_analytics_workspace_id"`
	VnetID                       string  `yaml:"vnet_id"`
	InfrastructureSubnetID       string  `yaml:"infrastructure_subnet_id"`
	PostgresqlSubnetID           *string `yaml:"postgresql_subnet_id"`
	MysqlSubnetID                *string `yaml:"mysql_subnet_id"`
	ContainerRegistryName        *string `yaml:"container_registry_name"`
	PostgresqlServerID           *string `yaml:"postgresql_server_id"`
	UserAssignedIdentityID       *string `yaml:"user_assigned_identity_id"`
	AzureEndpoint                *string `yaml:"azure_endpoint"`
}

// LoadAzureEnvironment loads an Azure environment YAML file, mirroring
// environment.py's load_environment for the "azure" target specifically.
func LoadAzureEnvironment(path string) (*models.AzureEnvironment, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read environment file %s: %w", path, err)
	}

	var parsed azureEnvironmentYAML
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse environment file %s: %w", path, err)
	}

	if parsed.Target == "" {
		parsed.Target = "azure"
	}
	if parsed.Target != "azure" {
		return nil, fmt.Errorf(
			"%s declares target %q; this loader only supports \"azure\"",
			path, parsed.Target,
		)
	}

	env := models.NewAzureEnvironment()
	env.Name = parsed.Name
	if parsed.Region != "" {
		env.Region = parsed.Region
	}
	if parsed.LogRetentionDays != nil {
		env.LogRetentionDays = *parsed.LogRetentionDays
	}
	if parsed.RetainDataOnDestroy != nil {
		env.RetainDataOnDestroy = *parsed.RetainDataOnDestroy
	}
	env.Tags = parsed.Tags
	env.ContainerAppsEnvironmentName = parsed.ContainerAppsEnvironmentName
	env.LogAnalyticsWorkspaceID = parsed.LogAnalyticsWorkspaceID
	env.VnetID = parsed.VnetID
	env.InfrastructureSubnetID = parsed.InfrastructureSubnetID
	env.PostgresqlSubnetID = parsed.PostgresqlSubnetID
	env.MysqlSubnetID = parsed.MysqlSubnetID
	env.ContainerRegistryName = parsed.ContainerRegistryName
	env.PostgresqlServerID = parsed.PostgresqlServerID
	env.UserAssignedIdentityID = parsed.UserAssignedIdentityID
	env.AzureEndpoint = parsed.AzureEndpoint

	// AzureEnvironment carries no cross-field validation in Python (no
	// model_validator equivalent to AwsEnvironment's ALB check) --
	// confirmed by the earlier survey of environment.py, not assumed.

	return &env, nil
}
