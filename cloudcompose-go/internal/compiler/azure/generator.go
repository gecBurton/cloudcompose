package azure

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// azureIngressFQDN is the Terraform reference to the hostname of the
// externally reachable app.
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

// azureCdnFQDN is the Terraform reference to the hostname of the first
// Front Door endpoint, if any service has cdn:true. Once traffic is
// fronted by Front Door, that hostname -- not the Container App's own
// ingress FQDN azureIngressFQDN already publishes -- is the address a
// real client should actually be sent to: the Container App's ingress
// FQDN keeps working directly (Front Door does not disable it), but
// bypasses the CDN/WAF layer entirely, which is exactly the case
// docs/azure-todo.md's Front Door item flagged as unverified -- a clean
// `terraform apply` had only ever been checked by polling the Container
// App's own FQDN, never Front Door's.
//
// host_name is azurerm_cdn_frontdoor_endpoint's own computed attribute
// (confirmed against the real provider schema, not assumed from the
// other resources' `name`/`id` pattern).
func azureCdnFQDN(resources *models.AzureResources) *string {
	keys := make([]string, 0, len(resources.CdnFrontdoorEndpoint))
	for k := range resources.CdnFrontdoorEndpoint {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil
	}
	sortStringsAzure(keys)
	fqdn := "${azurerm_cdn_frontdoor_endpoint." + keys[0] + ".host_name}"
	return &fqdn
}

// sortedStringKeysAzureApp returns ContainerApp map keys sorted, so
// azureIngressFQDN's "first" match is deterministic rather than dependent
// on Go's randomized map iteration order.
func sortedStringKeysAzureApp(m map[string]models.ContainerApp) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStringsAzure(keys)
	return keys
}

// GenerateAzure renders a Terraform JSON manifest for the given Azure
// resources and environment.
func GenerateAzure(resources *models.AzureResources, env *models.AzureEnvironment) (string, error) {
	requiredProviders := map[string]any{
		"azurerm": map[string]any{"source": "hashicorp/azurerm", "version": "~> 4.0"},
		"random":  map[string]any{"source": "hashicorp/random", "version": "~> 3.6"},
	}
	provider := map[string]any{
		"azurerm": map[string]any{"features": map[string]any{}},
		"random":  map[string]any{},
	}

	// Only wire up the docker provider if something actually builds an
	// image. Auth is against the ACR admin account
	// (azurerm_container_registry has admin_enabled=true whenever it
	// exists) rather than a short-lived token like AWS's
	// aws_ecr_authorization_token data source: ACR's admin credentials
	// are stable resource attributes, so no data source is needed to
	// fetch them.
	if len(resources.DockerImage) > 0 {
		requiredProviders["docker"] = map[string]any{"source": "kreuzwerker/docker", "version": "~> 3.0"}
		provider["docker"] = map[string]any{
			"registry_auth": []any{
				map[string]any{
					"address":  "${azurerm_container_registry.main.login_server}",
					"username": "${azurerm_container_registry.main.admin_username}",
					"password": "${azurerm_container_registry.main.admin_password}",
				},
			},
		}
	}

	// Only wire up the time provider if grantKeyVaultAccessOnce actually
	// created the RBAC-propagation time_sleep (see TimeSleep's own doc
	// comment) -- apps with no managed-service credentials never create
	// a Key Vault at all, and shouldn't declare a provider they have no
	// resource of.
	if len(resources.TimeSleep) > 0 {
		requiredProviders["time"] = map[string]any{"source": "hashicorp/time", "version": "~> 0.13"}
		provider["time"] = map[string]any{}
	}

	data := map[string]any{
		"azurerm_client_config": map[string]any{"current": map[string]any{}},
	}

	resourceBlocksMap, err := shared.StructResourceBlocks(resources)
	if err != nil {
		return "", err
	}

	manifest := models.TerraformManifest{
		Terraform: map[string]any{"required_providers": requiredProviders},
		Provider:  provider,
		Data:      data,
		Resource:  resourceBlocksMap,
	}

	// On AWS the public hostname belongs to the environment's shared load
	// balancer, so the environment stack publishes it. A Container App
	// carries its own ingress hostname, so it has to be published here or
	// nothing downstream can reach the deployed application.
	if fqdn := azureIngressFQDN(resources); fqdn != nil {
		manifest.Output = map[string]any{
			"fqdn": map[string]any{
				"description": "Public hostname of the ingress-enabled service.",
				"value":       *fqdn,
			},
		}
	}

	// Only present when a service has cdn:true (azureCdnFQDN returns nil
	// otherwise). See azureCdnFQDN's own doc comment for why this is a
	// distinct output from "fqdn" above, not a replacement for it.
	if cdnFqdn := azureCdnFQDN(resources); cdnFqdn != nil {
		if manifest.Output == nil {
			manifest.Output = map[string]any{}
		}
		manifest.Output["cdn_fqdn"] = map[string]any{
			"description": "Public hostname of the Front Door endpoint fronting the CDN-enabled service.",
			"value":       *cdnFqdn,
		}
	}

	return shared.MarshalIndentedJSON(manifest)
}
