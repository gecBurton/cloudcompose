package azure

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// GenerateAzureEnvironment generates Terraform JSON for a shared Azure
// environment. Creates a Resource Group, Log Analytics Workspace, and a
// VNet -- the Cloud Compose Environment layer: policy (log/backup
// retention, HA, region) and the network address space apps live in, not
// the apps themselves.
//
// Does NOT create a Container Apps Environment or any subnets: those
// are per-app now, created by GenerateAzure (cloudcompose main), one
// set per app inside this VNet -- see
// docs/azure-app-isolation-design.md for why. A Container Apps
// Environment is Azure's actual isolation boundary (confirmed against
// the real azurerm_container_app schema, which has no networking fields
// at all, and Microsoft's own docs: "Use more than one environment when
// you want two or more applications to... never share the same compute
// resources"), so a shared one here would defeat the isolation this
// design exists to provide, the same way sharing one AWS security group
// across unrelated apps would.
//
// The environment's facts are exposed as a plain Terraform
// `output "environment"` block only -- see aws.GenerateAwsEnvironment's
// own doc comment for why.
//
// backend, if non-nil, is emitted both as this environment's own
// `terraform { backend "azurerm" {...} }` block (state key derived from
// name via shared.BackendKeyForEnvironment -- never authored) and as a
// plain `output "backend"` block, mirroring
// aws.GenerateAwsEnvironment's own backend handling exactly -- see
// docs/multi-user-state.md.
func GenerateAzureEnvironment(
	name, location, vnetCIDR string,
	tags map[string]string,
	retainDataOnDestroy bool,
	highAvailabilityEnabled bool,
	backupRetentionDays int,
	logRetentionDays int,
	backend *models.BackendConfig,
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

	// backendConfig, if set, is also emitted verbatim into the
	// generated `output "backend"` block below, so LoadAzureEnvironment
	// can hand it back to `cloudcompose compile`, which reuses it --
	// under a different, app-specific key -- for every app compiled
	// against this environment. See
	// aws.GenerateAwsEnvironment's identical handling and
	// docs/multi-user-state.md.
	var backendConfig map[string]any
	if backend != nil && backend.Azure != nil {
		azurermBackend := map[string]any{
			"resource_group_name":  backend.Azure.ResourceGroupName,
			"storage_account_name": backend.Azure.StorageAccountName,
			"container_name":       backend.Azure.ContainerName,
			"key":                  shared.BackendKeyForEnvironment(name),
		}
		useAzureADAuth := true
		if backend.Azure.UseAzureADAuth != nil {
			useAzureADAuth = *backend.Azure.UseAzureADAuth
		}
		azurermBackend["use_azuread_auth"] = useAzureADAuth
		terraform["backend"] = map[string]any{"azurerm": azurermBackend}

		backendConfig = map[string]any{
			"resource_group_name":  backend.Azure.ResourceGroupName,
			"storage_account_name": backend.Azure.StorageAccountName,
			"container_name":       backend.Azure.ContainerName,
			"use_azuread_auth":     useAzureADAuth,
		}
	}
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

	// appsCIDR is the upper half of the VNet, reserved for apps -- see
	// docs/azure-app-isolation-design.md's "Decided: CIDR math" section
	// for the full reasoning (128 apps at the default VNet size, each
	// app's own /24 split into four /26 subnets, double Container
	// Apps' own documented /27 minimum). The lower half is implicitly
	// reserved for whatever the Cloud Compose Environment layer itself
	// might need in its own address space in the future -- nothing
	// uses it today, but reserving it now costs nothing and avoids a
	// second breaking change later if something does.
	appsCIDR, err := shared.Cidrsubnet(vnetCIDR, 1, 1)
	if err != nil {
		return "", err
	}

	environmentConfig := map[string]any{
		"target":                     "azure",
		"name":                       name,
		"region":                     location,
		"log_analytics_workspace_id": fmt.Sprintf("${azurerm_log_analytics_workspace.%s.id}", tfn),
		"resource_group_name":        fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn),
		"vnet_id":                    fmt.Sprintf("${azurerm_virtual_network.%s.id}", tfn),
		"vnet_name":                  fmt.Sprintf("${azurerm_virtual_network.%s.name}", tfn),
		"apps_cidr":                  appsCIDR,
		"retain_data_on_destroy":     retainDataOnDestroy,
		"high_availability_enabled":  highAvailabilityEnabled,
		"backup_retention_days":      backupRetentionDays,
		"log_retention_days":         workspaceRetentionDays,
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
	if backendConfig != nil {
		outputs["backend"] = map[string]any{
			"description": "This environment's own backend config (provider name plus resource group/storage account/container), so every app compiled against this environment can derive its own backend under the same storage account. See docs/multi-user-state.md.",
			"value":       map[string]any{"provider": "azure", "azure": backendConfig},
		}
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
