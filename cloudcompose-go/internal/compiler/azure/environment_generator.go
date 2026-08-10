package azure

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
)

// GenerateAzureEnvironment generates Terraform JSON for a shared Azure
// environment. Creates a Resource Group, Log Analytics Workspace, VNet
// with four subnets (Container
// Apps, PostgreSQL, MySQL -- each delegated -- and Redis, a plain
// subnet for a Managed Redis private endpoint, added 2026-08-08, see
// docs/azure-aws-parity-todo.md's Priority 3 Redis private networking
// item), and a Container Apps Environment.
//
// The environment's facts are exposed as a plain Terraform
// `output "environment"` block only -- see aws.GenerateAwsEnvironment's
// own doc comment for why.
func GenerateAzureEnvironment(
	name, location, vnetCIDR string,
	tags map[string]string,
	retainDataOnDestroy bool,
	highAvailabilityEnabled bool,
	backupRetentionDays int,
	logRetentionDays int,
) (string, error) {
	tfn := shared.TfName(name)
	envTag := map[string]string{"Environment": name}

	// azurerm_log_analytics_workspace.retention_in_days has a hard
	// minimum of 30 days -- confirmed against the real provider schema
	// (`terraform validate` itself rejects anything lower, "expected
	// retention_in_days to be in the range (30 - 730)"), not a matter of
	// preference the way the shared 7-day default otherwise is. AWS's
	// CloudWatch equivalent has no such floor (its own minimum is 1
	// day), so log_retention_days' shared default of 7 -- chosen to
	// match AWS's own long-standing default, unrelated to Azure's
	// constraint -- would silently break every Azure environment that
	// doesn't override it. Clamped here, not by raising the shared
	// default: AWS keeps whatever value a user actually asked for.
	// The output block below reports this clamped value too, not the
	// raw request: it's meant to reflect what's actually deployed, and
	// a silent mismatch between the output and the real resource would
	// be its own confusing bug for whatever later reads it back via
	// LoadAzureEnvironment.
	workspaceRetentionDays := logRetentionDays
	if workspaceRetentionDays < 30 {
		workspaceRetentionDays = 30
	}

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
			"retention_in_days":   workspaceRetentionDays,
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
	redisCIDR, err := shared.Cidrsubnet(vnetCIDR, 5, 3)
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
		// Not delegated: azurerm_private_endpoint (used for Managed
		// Redis, see permissions.go/managed.go's privateEndpoint
		// helpers) attaches to a plain subnet, unlike the delegated
		// subnets Flexible Server needs above.
		tfn + "_redis": map[string]any{
			"name":                 "redis",
			"resource_group_name":  fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn),
			"virtual_network_name": fmt.Sprintf("${azurerm_virtual_network.%s.name}", tfn),
			"address_prefixes":     []string{redisCIDR},
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
		"redis_subnet_id":                 fmt.Sprintf("${azurerm_subnet.%s_redis.id}", tfn),
		"retain_data_on_destroy":          retainDataOnDestroy,
		"high_availability_enabled":       highAvailabilityEnabled,
		"backup_retention_days":           backupRetentionDays,
		"log_retention_days":              workspaceRetentionDays,
	}
	if len(tags) > 0 {
		environmentConfig["tags"] = tags
	}

	outputs := map[string]any{
		"environment": map[string]any{
			"description": "Values matching cloudcompose's Environment model.",
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
