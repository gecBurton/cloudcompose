package azure

import (
	"fmt"

	"github.com/gecburton/composey/internal/compiler/shared"
)

// GenerateAzureEnvironment generates Terraform JSON for a shared Azure
// environment, mirroring environment_generator.py. Creates a Resource
// Group, Log Analytics Workspace, VNet with three delegated subnets
// (Container Apps, PostgreSQL, MySQL), and a Container Apps Environment.
//
// The environment's facts are exposed as a plain Terraform
// `output "environment"` block only -- see aws.GenerateAwsEnvironment's
// own doc comment for why.
func GenerateAzureEnvironment(
	name, location, vnetCIDR string,
	tags map[string]string,
	retainDataOnDestroy bool,
) (string, error) {
	tfn := shared.TfName(name)
	envTag := map[string]string{"Environment": name}

	requiredProviders := map[string]any{
		"azurerm": map[string]any{"source": "hashicorp/azurerm", "version": "~> 4.0"},
	}
	terraform := map[string]any{"required_version": ">= 1.5", "required_providers": requiredProviders}
	provider := map[string]any{"azurerm": map[string]any{"features": map[string]any{}}}
	dataSources := map[string]any{"azurerm_client_config": map[string]any{"current": map[string]any{}}}

	resource := map[string]any{}

	registerCmd := "az provider register --namespace Microsoft.OperationalInsights --wait && " +
		"az provider register --namespace Microsoft.ContainerInstance --wait && " +
		"az provider register --namespace Microsoft.App --wait && " +
		"az provider register --namespace Microsoft.Network --wait"
	resource["null_resource"] = map[string]any{
		tfn + "_register_providers": map[string]any{
			"provisioner": []any{
				map[string]any{
					"local-exec": map[string]any{
						"command":     registerCmd,
						"interpreter": []string{"/bin/sh", "-c"},
					},
				},
			},
		},
	}

	resource["azurerm_resource_group"] = map[string]any{
		tfn: map[string]any{
			"name":     name,
			"location": location,
			"tags":     shared.MergedTags(tags, envTag),
		},
	}

	resource["azurerm_log_analytics_workspace"] = map[string]any{
		tfn: map[string]any{
			"name":                name + "-logs",
			"location":            location,
			"resource_group_name": fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn),
			"sku":                 "PerGB2018",
			"retention_in_days":   30,
			"tags":                shared.MergedTags(tags, envTag),
		},
	}

	resource["azurerm_virtual_network"] = map[string]any{
		tfn: map[string]any{
			"name":                name + "-vnet",
			"location":            location,
			"resource_group_name": fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn),
			"address_space":       []string{vnetCIDR},
			"tags":                shared.MergedTags(tags, envTag),
		},
	}

	infraCIDR, err := shared.Cidrsubnet(vnetCIDR, 5, 0)
	if err != nil {
		return "", err
	}
	pgCIDR, err := shared.Cidrsubnet(vnetCIDR, 5, 1)
	if err != nil {
		return "", err
	}
	mysqlCIDR, err := shared.Cidrsubnet(vnetCIDR, 5, 2)
	if err != nil {
		return "", err
	}

	delegation := func(delegationName, serviceName string) []any {
		return []any{map[string]any{
			"name": delegationName,
			"service_delegation": []any{map[string]any{
				"name":    serviceName,
				"actions": []string{"Microsoft.Network/virtualNetworks/subnets/join/action"},
			}},
		}}
	}

	resource["azurerm_subnet"] = map[string]any{
		tfn + "_infrastructure": map[string]any{
			"name":                 "infrastructure",
			"resource_group_name":  fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn),
			"virtual_network_name": fmt.Sprintf("${azurerm_virtual_network.%s.name}", tfn),
			"address_prefixes":     []string{infraCIDR},
			"delegation":           delegation("container-apps", "Microsoft.App/environments"),
		},
		tfn + "_postgresql": map[string]any{
			"name":                 "postgresql",
			"resource_group_name":  fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn),
			"virtual_network_name": fmt.Sprintf("${azurerm_virtual_network.%s.name}", tfn),
			"address_prefixes":     []string{pgCIDR},
			"delegation":           delegation("postgresql-flexible-server", "Microsoft.DBforPostgreSQL/flexibleServers"),
		},
		tfn + "_mysql": map[string]any{
			"name":                 "mysql",
			"resource_group_name":  fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn),
			"virtual_network_name": fmt.Sprintf("${azurerm_virtual_network.%s.name}", tfn),
			"address_prefixes":     []string{mysqlCIDR},
			"delegation":           delegation("mysql-flexible-server", "Microsoft.DBforMySQL/flexibleServers"),
		},
	}

	resource["azurerm_container_app_environment"] = map[string]any{
		tfn: map[string]any{
			"name":                       name + "-env",
			"location":                   location,
			"resource_group_name":        fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn),
			"log_analytics_workspace_id": fmt.Sprintf("${azurerm_log_analytics_workspace.%s.id}", tfn),
			"infrastructure_subnet_id":   fmt.Sprintf("${azurerm_subnet.%s_infrastructure.id}", tfn),
			"tags":                       shared.MergedTags(tags, envTag),
		},
	}

	environmentConfig := map[string]any{
		"target":                          "azure",
		"name":                            name,
		"region":                          location,
		"container_apps_environment_name": fmt.Sprintf("${azurerm_container_app_environment.%s.name}", tfn),
		"log_analytics_workspace_id":      fmt.Sprintf("${azurerm_log_analytics_workspace.%s.id}", tfn),
		"vnet_id":                         fmt.Sprintf("${azurerm_virtual_network.%s.id}", tfn),
		"infrastructure_subnet_id":        fmt.Sprintf("${azurerm_subnet.%s_infrastructure.id}", tfn),
		"postgresql_subnet_id":            fmt.Sprintf("${azurerm_subnet.%s_postgresql.id}", tfn),
		"mysql_subnet_id":                 fmt.Sprintf("${azurerm_subnet.%s_mysql.id}", tfn),
		"retain_data_on_destroy":          retainDataOnDestroy,
	}
	if len(tags) > 0 {
		environmentConfig["tags"] = tags
	}

	outputs := map[string]any{
		"environment": map[string]any{
			"description": "Values matching composey's Environment model.",
			"value":       environmentConfig,
		},
	}

	manifest := map[string]any{
		"terraform": terraform,
		"provider":  provider,
		"data":      dataSources,
		"resource":  resource,
		"output":    outputs,
	}

	return shared.MarshalIndentedJSON(manifest)
}
