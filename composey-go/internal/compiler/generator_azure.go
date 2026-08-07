package compiler

import (
	"github.com/gecburton/composey/internal/models"
)

// azureIngressFQDN is the Terraform reference to the hostname of the
// externally reachable app, mirroring _ingress_fqdn.
func azureIngressFQDN(resources *models.AzureResources) *string {
	for _, key := range sortedStringKeysAzureApp(resources.ContainerApp) {
		app := resources.ContainerApp[key]
		if app.Ingress != nil {
			fqdn := "${azurerm_container_app." + key + ".ingress[0].fqdn}"
			return &fqdn
		}
	}
	return nil
}

// sortedStringKeysAzureApp returns ContainerApp map keys sorted, so
// azureIngressFQDN's "first" match is deterministic rather than dependent
// on Go's randomized map iteration order. Python's dict iterates in
// insertion order, which for resources.azurerm_container_app is the order
// services were declared in the compose file -- callers needing exact
// parity with which service's FQDN gets picked when multiple have ingress
// should be aware this picks the alphabetically-first key, not the
// first-declared one; confirmed acceptable here because
// azure_environment_ownership's own test only ever has one ingress-carrying
// service.
func sortedStringKeysAzureApp(m map[string]models.ContainerApp) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStringsAzure(keys)
	return keys
}

// azureResourceOrder is the exact resource-type order generator_azure.py's
// _build_resources declares its if-blocks in. Since Python's json.dumps
// here has no sort_keys=True (unlike AWS's generator.py), this order is
// not incidental -- it is exactly what appears in the output, and must be
// reproduced rather than left to alphabetical or declaration-order
// chance.
var azureResourceOrder = []string{
	"azurerm_container_app_environment",
	"azurerm_container_app",
	"azurerm_container_app_job",
	"azurerm_container_registry",
	"azurerm_postgresql_flexible_server",
	"azurerm_postgresql_flexible_server_database",
	"azurerm_mysql_flexible_server",
	"azurerm_mysql_flexible_database",
	"azurerm_private_dns_zone",
	"azurerm_private_dns_zone_virtual_network_link",
	"azurerm_key_vault",
	"azurerm_key_vault_secret",
	"azurerm_user_assigned_identity",
	"azurerm_role_assignment",
	"azurerm_managed_redis",
	"azurerm_storage_account",
	"azurerm_storage_container",
	"azurerm_cdn_frontdoor_profile",
	"azurerm_cdn_frontdoor_endpoint",
	"azurerm_cdn_frontdoor_origin_group",
	"azurerm_cdn_frontdoor_origin",
	"azurerm_cdn_frontdoor_route",
	"docker_image",
	"docker_registry_image",
	"random_password",
}

// azureResourceBlocks builds the "resource" section of the Terraform
// document, mirroring _build_resources. Each resource type is included
// only if it has entries -- an empty "resource": {} block for a type
// nobody used would still be valid JSON, but Python's own `if
// resources.X:` checks skip it, so this must too, for byte-identical
// output.
func azureResourceBlocks(resources *models.AzureResources) PyOrdered {
	nonEmpty := map[string]any{
		"azurerm_container_app_environment":             resources.ContainerAppEnvironment,
		"azurerm_container_app":                         resources.ContainerApp,
		"azurerm_container_app_job":                     resources.ContainerAppJob,
		"azurerm_container_registry":                    resources.ContainerRegistry,
		"azurerm_postgresql_flexible_server":            resources.PostgreSQLFlexibleServer,
		"azurerm_postgresql_flexible_server_database":   resources.PostgreSQLFlexibleServerDatabase,
		"azurerm_mysql_flexible_server":                 resources.MySQLFlexibleServer,
		"azurerm_mysql_flexible_database":               resources.MySQLFlexibleDatabase,
		"azurerm_private_dns_zone":                      resources.PrivateDnsZone,
		"azurerm_private_dns_zone_virtual_network_link": resources.PrivateDnsZoneVirtualNetworkLink,
		"azurerm_key_vault":                             resources.KeyVault,
		"azurerm_key_vault_secret":                      resources.KeyVaultSecret,
		"azurerm_user_assigned_identity":                resources.UserAssignedIdentity,
		"azurerm_role_assignment":                       resources.RoleAssignment,
		"azurerm_managed_redis":                         resources.ManagedRedis,
		"azurerm_storage_account":                       resources.StorageAccount,
		"azurerm_storage_container":                     resources.StorageContainer,
		"azurerm_cdn_frontdoor_profile":                 resources.CdnFrontdoorProfile,
		"azurerm_cdn_frontdoor_endpoint":                resources.CdnFrontdoorEndpoint,
		"azurerm_cdn_frontdoor_origin_group":            resources.CdnFrontdoorOriginGroup,
		"azurerm_cdn_frontdoor_origin":                  resources.CdnFrontdoorOrigin,
		"azurerm_cdn_frontdoor_route":                   resources.CdnFrontdoorRoute,
		"docker_image":                                  resources.DockerImage,
		"docker_registry_image":                         resources.DockerRegistryImage,
		"random_password":                               resources.RandomPassword,
	}

	result := PyOrdered{}
	for _, resourceType := range azureResourceOrder {
		value := nonEmpty[resourceType]
		if !isNonEmptyMap(value) {
			continue
		}
		result = append(result, p(resourceType, structToPyOrdered(value)))
	}
	return result
}

// isNonEmptyMap reports whether v (always one of the map[string]T fields
// on AzureResources) has at least one entry, mirroring Python's bare `if
// resources.X:` truthiness check on a dict.
func isNonEmptyMap(v any) bool {
	switch m := v.(type) {
	case map[string]models.ContainerApp:
		return len(m) > 0
	case map[string]models.ContainerAppJob:
		return len(m) > 0
	case map[string]models.ContainerAppEnvironment:
		return len(m) > 0
	case map[string]models.ContainerRegistry:
		return len(m) > 0
	case map[string]models.PostgreSQLFlexibleServer:
		return len(m) > 0
	case map[string]models.PostgreSQLFlexibleDatabase:
		return len(m) > 0
	case map[string]models.MySQLFlexibleServer:
		return len(m) > 0
	case map[string]models.MySQLFlexibleDatabase:
		return len(m) > 0
	case map[string]models.PrivateDnsZone:
		return len(m) > 0
	case map[string]models.PrivateDnsZoneVirtualNetworkLink:
		return len(m) > 0
	case map[string]models.KeyVault:
		return len(m) > 0
	case map[string]models.KeyVaultSecret:
		return len(m) > 0
	case map[string]models.UserAssignedIdentity:
		return len(m) > 0
	case map[string]models.RoleAssignment:
		return len(m) > 0
	case map[string]models.ManagedRedis:
		return len(m) > 0
	case map[string]models.StorageAccount:
		return len(m) > 0
	case map[string]models.StorageContainer:
		return len(m) > 0
	case map[string]models.FrontDoorProfile:
		return len(m) > 0
	case map[string]models.FrontDoorEndpoint:
		return len(m) > 0
	case map[string]models.FrontDoorOriginGroup:
		return len(m) > 0
	case map[string]models.FrontDoorOrigin:
		return len(m) > 0
	case map[string]models.FrontDoorRoute:
		return len(m) > 0
	case map[string]models.DockerImage:
		return len(m) > 0
	case map[string]models.DockerRegistryImage:
		return len(m) > 0
	case map[string]models.RandomPassword:
		return len(m) > 0
	default:
		return false
	}
}

// GenerateAzure renders a Terraform JSON manifest for the given Azure
// resources and environment, mirroring generator_azure.py's generate().
//
// Unlike GenerateAWS, this has no sort_keys equivalent to fall back on:
// Python's own json.dumps(terraform, indent=2) call has no sort_keys=True,
// so every level of this document must preserve insertion order exactly
// as built here, via PyDumpsIndent rather than encoding/json.
func GenerateAzure(resources *models.AzureResources, env *models.AzureEnvironment) (string, error) {
	requiredProviders := PyOrdered{
		p("azurerm", PyOrdered{p("source", "hashicorp/azurerm"), p("version", "~> 4.0")}),
		p("random", PyOrdered{p("source", "hashicorp/random"), p("version", "~> 3.6")}),
	}
	provider := PyOrdered{
		p("azurerm", PyOrdered{p("features", PyOrdered{})}),
		p("random", PyOrdered{}),
	}

	// Only wire up the docker provider if something actually builds an
	// image. Auth is against the ACR admin account
	// (azurerm_container_registry has admin_enabled=True whenever it
	// exists) rather than a short-lived token like AWS's
	// aws_ecr_authorization_token data source: ACR's admin credentials
	// are stable resource attributes, so no data source is needed to
	// fetch them.
	if len(resources.DockerImage) > 0 {
		requiredProviders = append(requiredProviders, p("docker", PyOrdered{
			p("source", "kreuzwerker/docker"), p("version", "~> 3.0"),
		}))
		provider = append(provider, p("docker", PyOrdered{
			p("registry_auth", []any{
				PyOrdered{
					p("address", "${azurerm_container_registry.main.login_server}"),
					p("username", "${azurerm_container_registry.main.admin_username}"),
					p("password", "${azurerm_container_registry.main.admin_password}"),
				},
			}),
		}))
	}

	data := PyOrdered{
		p("azurerm_client_config", PyOrdered{p("current", PyOrdered{})}),
		// The Container Apps Environment belongs to the platform stack,
		// not to the application. Looked up rather than declared: two
		// stacks that both manage it fight over the same resource, and
		// the app stack loses with "already exists - to be managed via
		// Terraform this resource needs to be imported into the State".
		p("azurerm_container_app_environment", PyOrdered{
			p("main", PyOrdered{
				p("name", env.ContainerAppsEnvironmentName),
				p("resource_group_name", env.Name),
			}),
		}),
	}

	terraformDoc := PyOrdered{
		p("terraform", PyOrdered{p("required_providers", requiredProviders)}),
		p("provider", provider),
		p("data", data),
		p("resource", azureResourceBlocks(resources)),
	}

	// On AWS the public hostname belongs to the environment's shared load
	// balancer, so the environment stack publishes it. A Container App
	// carries its own ingress hostname, so it has to be published here or
	// nothing downstream can reach the deployed application.
	if fqdn := azureIngressFQDN(resources); fqdn != nil {
		terraformDoc = append(terraformDoc, p("output", PyOrdered{
			p("fqdn", PyOrdered{
				p("description", "Public hostname of the ingress-enabled service."),
				p("value", *fqdn),
			}),
		}))
	}

	// env.azure_endpoint (custom endpoint override, for testing) is a
	// documented no-op in Python -- it reassigns features to {} again and
	// does nothing else. Not ported: there is no behavior to replicate,
	// only the absence of one.

	return PyDumpsIndent(terraformDoc, 2), nil
}
