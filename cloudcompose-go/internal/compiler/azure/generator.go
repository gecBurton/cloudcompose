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
// Front Door endpoint, if any service has cdn:true. This is a distinct
// output from azureIngressFQDN: the Container App's own ingress FQDN
// keeps working directly but bypasses the CDN/WAF layer, so a client
// should be sent to Front Door's hostname instead once it exists.
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

// azureKeyVaultName is the Terraform reference to the Key Vault's `name`
// attribute, published so callers outside Terraform can query the
// vault's data plane directly without re-deriving KeyVaultName's naming
// scheme. Only present when a Key Vault exists.
func azureKeyVaultName(resources *models.AzureResources) *string {
	if _, ok := resources.KeyVault["main"]; !ok {
		return nil
	}
	name := "${azurerm_key_vault.main.name}"
	return &name
}

// sortedStringKeysAzureApp returns ContainerApp map keys sorted, so
// azureIngressFQDN's "first" match is deterministic.
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
func GenerateAzure(resources *models.AzureResources, env *models.AzureEnvironment, projectName string) (string, error) {
	requiredProviders := map[string]any{
		"azurerm": map[string]any{"source": "hashicorp/azurerm", "version": "~> 4.0"},
		"random":  map[string]any{"source": "hashicorp/random", "version": "~> 3.6"},
	}
	provider := map[string]any{
		"azurerm": map[string]any{"features": map[string]any{}},
		"random":  map[string]any{},
	}

	// Only wire up the docker provider if something actually builds an
	// image. Auth is against the ACR admin account, whose credentials
	// are stable resource attributes.
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
	// created the RBAC-propagation time_sleep.
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
	if backendBlock := shared.AppBackendBlock(env.Name, projectName, env.Backend); backendBlock != nil {
		manifest.Terraform["backend"] = backendBlock
	}

	// A Container App carries its own ingress hostname, so it has to be
	// published here.
	if fqdn := azureIngressFQDN(resources); fqdn != nil {
		manifest.Output = map[string]any{
			"fqdn": map[string]any{
				"description": "Public hostname of the ingress-enabled service.",
				"value":       *fqdn,
			},
		}
	}

	// Only present when a service has cdn:true.
	if cdnFqdn := azureCdnFQDN(resources); cdnFqdn != nil {
		if manifest.Output == nil {
			manifest.Output = map[string]any{}
		}
		manifest.Output["cdn_fqdn"] = map[string]any{
			"description": "Public hostname of the Front Door endpoint fronting the CDN-enabled service.",
			"value":       *cdnFqdn,
		}
	}

	// Only present when a Key Vault exists.
	if kvName := azureKeyVaultName(resources); kvName != nil {
		if manifest.Output == nil {
			manifest.Output = map[string]any{}
		}
		manifest.Output["key_vault_name"] = map[string]any{
			"description": "Name of the Key Vault holding managed-service/secrets/config credentials, if any.",
			"value":       *kvName,
		}
	}

	return shared.MarshalIndentedJSON(manifest)
}
