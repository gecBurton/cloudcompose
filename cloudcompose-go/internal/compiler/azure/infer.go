package azure

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// InferAzure is the main entry point for Azure inference.
func InferAzure(app *models.Application, env *models.AzureEnvironment) (*models.AzureResources, error) {
	resources := models.NewAzureResources()

	getName := shared.ResourceNamer(env.Name, app.Name)
	var tags map[string]string
	if len(env.Tags) > 0 {
		tags = env.Tags
	}

	// Step 0: create this app's Container Apps Environment and its four
	// delegated subnets, carved out of the environment's shared AppsCIDR
	// at this app's subnet-index. Must run before anything that reads
	// env.InfrastructureSubnetID/PostgresqlSubnetID/MysqlSubnetID/RedisSubnetID.
	if err := appSubnetsAzure(resources, app, env, getName, tags); err != nil {
		return nil, err
	}

	// Step 1: Create managed identity (or use existing).
	identityID := inferManagedIdentity(env)

	// Step 3: Create Key Vault for secrets.
	inferKeyVault(resources, app, env, tags)

	// Step 4: Create container registry (if building from source).
	inferContainerRegistry(resources, app, env, tags)

	// Step 5: Infer database resources.
	connections := inferDatabasesAzure(resources, app, env, getName, tags)

	// Step 6: Infer cache resources.
	for k, v := range inferCachesAzure(resources, app, env, getName, tags) {
		connections[k] = v
	}

	// Step 7: Infer storage resources.
	for k, v := range inferStorageAzure(resources, app, env, getName, tags) {
		connections[k] = v
	}

	// Step 7.5: discover which connections each service's authored
	// `environment:` values actually reference. Must happen before
	// deciding whether any service needs a managed-service identity,
	// since the identity must exist and be granted access before any
	// Container App/Job that references it.
	referenced := referencedServersAzure(app, connections)

	// Step 7.6: if any service references a managed service, create a
	// user-assigned identity for the app and grant it access to that
	// service's stored credential/storage account. Only services that
	// reference a connection use this identity; every other service
	// keeps using env.UserAssignedIdentityID or falls back to
	// system-assigned.
	managedServiceIdentityID := inferManagedServiceIdentity(resources, app, env, getName, tags, connections, referenced)
	if managedServiceIdentityID != "" {
		grantManagedServicePermissions(resources, app, env, getName, tags, managedServiceIdentityID, connections, referenced)
	}

	// connectionOrder tracks connections in insertion order (databases,
	// then caches, then storage, each in app.Services order): this
	// determines which _URL env var comes first when a service
	// references more than one connection.
	connectionOrder := shared.ConnectionOrder(app, connections)

	// Step 8: Infer container apps.
	if err := inferContainerApps(resources, app, env, getName, tags, identityID, managedServiceIdentityID, connections, connectionOrder, referenced); err != nil {
		return nil, err
	}

	// Step 9: Scheduled services run as Jobs, not as always-on Container Apps.
	if err := inferScheduledJobs(resources, app, env, getName, tags, identityID, managedServiceIdentityID, connections, connectionOrder, referenced); err != nil {
		return nil, err
	}

	// Step 10: Infer CDN for services with cdn_enabled.
	inferCdnAzure(resources, app, env, getName, tags)

	return resources, nil
}

// inferManagedIdentity returns the configured identity resource ID, or ""
// if none is configured (a system-assigned identity is used implicitly
// instead).
func inferManagedIdentity(env *models.AzureEnvironment) string {
	if env.UserAssignedIdentityID != nil && *env.UserAssignedIdentityID != "" {
		return *env.UserAssignedIdentityID
	}
	return ""
}

// inferKeyVault creates the Key Vault for secrets. Always creates exactly
// one, keyed "main".
func inferKeyVault(resources *models.AzureResources, app *models.Application, env *models.AzureEnvironment, tags map[string]string) {
	kv := models.NewKeyVault()
	kv.Name = KeyVaultName(env.Name, app.Name)
	kv.ResourceGroupName = env.Name
	kv.Location = env.Region
	kv.TenantID = "${data.azurerm_client_config.current.tenant_id}"
	kv.Tags = tags
	resources.KeyVault["main"] = kv
}

// inferContainerRegistry creates the Azure Container Registry if any
// service builds from source.
func inferContainerRegistry(resources *models.AzureResources, app *models.Application, env *models.AzureEnvironment, tags map[string]string) {
	needsRegistry := false
	for i := range app.Services {
		s := &app.Services[i]
		if s.Capability == models.CapabilityContainer && s.BuildContext != nil {
			needsRegistry = true
			break
		}
	}
	if !needsRegistry {
		return
	}

	registryName := ContainerRegistryName(env.Name, app.Name)
	if env.ContainerRegistryName != nil && *env.ContainerRegistryName != "" {
		registryName = *env.ContainerRegistryName
	}

	registry := models.NewContainerRegistry()
	registry.Name = registryName
	registry.ResourceGroupName = env.Name
	registry.Location = env.Region
	registry.Sku = "Standard"
	// Required for Container Apps to pull, and for the docker provider to
	// authenticate when pushing.
	registry.AdminEnabled = true
	registry.Tags = tags
	resources.ContainerRegistry["main"] = registry

	// Build and push an image for every service with a build context. The
	// container's image reference is pinned to the pushed digest rather
	// than a mutable ":latest" tag, so Container Apps deploys exactly what
	// was built.
	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer || service.BuildContext == nil {
			continue
		}

		build := map[string]any{
			"context":  *service.BuildContext,
			"platform": "linux/amd64",
		}
		if service.Dockerfile != nil {
			build["dockerfile"] = *service.Dockerfile
		}

		imageKey := service.Name + "_image"
		resources.DockerImage[imageKey] = models.DockerImage{
			Name:  fmt.Sprintf("${azurerm_container_registry.main.login_server}/%s:latest", service.Name),
			Build: build,
		}

		pushKey := service.Name + "_push"
		push := models.NewDockerRegistryImage()
		push.Name = fmt.Sprintf("${docker_image.%s.name}", imageKey)
		resources.DockerRegistryImage[pushKey] = push
	}
}
