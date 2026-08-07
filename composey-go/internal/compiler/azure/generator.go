package azure

import (
	"github.com/gecburton/composey/internal/compiler/shared"
	"github.com/gecburton/composey/internal/models"
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

	data := map[string]any{
		"azurerm_client_config": map[string]any{"current": map[string]any{}},
		// The Container Apps Environment belongs to the platform stack,
		// not to the application. Looked up rather than declared: two
		// stacks that both manage it fight over the same resource, and
		// the app stack loses with "already exists - to be managed via
		// Terraform this resource needs to be imported into the State".
		"azurerm_container_app_environment": map[string]any{
			"main": map[string]any{
				"name":                env.ContainerAppsEnvironmentName,
				"resource_group_name": env.Name,
			},
		},
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

	return shared.MarshalIndentedJSON(manifest)
}
