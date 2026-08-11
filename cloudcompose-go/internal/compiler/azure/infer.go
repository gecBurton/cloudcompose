package azure

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/models"
)

// InferAzure is the main entry point for Azure inference.
func InferAzure(app *models.Application, env *models.AzureEnvironment) (*models.AzureResources, error) {
	resources := models.NewAzureResources()

	getName := func(resourceName string) string {
		return env.Name + "-" + app.Name + "-" + resourceName
	}
	var tags map[string]string
	if len(env.Tags) > 0 {
		tags = env.Tags
	}

	// Step 0: create this app's own Container Apps Environment and its
	// four delegated subnets, carved out of the environment's shared
	// AppsCIDR at this app's own --subnet-index. Must run before
	// anything below that reads env.InfrastructureSubnetID/
	// PostgresqlSubnetID/MysqlSubnetID/RedisSubnetID (databases, caches,
	// the Container App/Job resources themselves) -- see
	// docs/azure-app-isolation-design.md for why this moved here from
	// cloudcompose init, and appSubnetsAzure's own doc comment for the
	// CIDR math.
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

	// Step 7.5: if any service has a Relationship to a managed service,
	// create a user-assigned identity for the app (before any Container
	// App/Job exists, so it can be granted RBAC first -- see
	// permissions.go's own doc comments for the ordering reason) and
	// grant it access to that service's stored credential/storage
	// account. Only services that actually have such a Relationship use
	// this identity (see managedIdentityForService in compute.go); every
	// other service keeps using env.UserAssignedIdentityID or falls back
	// to system-assigned, exactly as before this feature existed.
	managedServiceIdentityID := inferManagedServiceIdentity(resources, app, env, getName, tags, connections)
	if managedServiceIdentityID != "" {
		grantManagedServicePermissions(resources, app, env, getName, tags, managedServiceIdentityID, connections)
	}

	// connectionOrder tracks connections in the same insertion order they
	// were built in: databases first, then caches, then storage -- each
	// group in the order its services appear in app.Services -- since
	// containerSpecAzure's env-var loop iterates connections in whatever
	// order they were inserted, and that order determines which _URL env
	// var comes first when a service references more than one. Confirmed
	// as a real, not theoretical, requirement by diffing actual output for
	// the doctor and production-stack examples against their golden
	// files (2026-08-06): DB_URL/BLOBS_URL and CACHE_URL appeared in the
	// wrong relative position under simple alphabetical-key sorting.
	connectionOrder := connectionOrderForAzure(app, connections)

	// Step 8: Infer container apps.
	if err := inferContainerApps(resources, app, env, getName, tags, identityID, managedServiceIdentityID, connections, connectionOrder); err != nil {
		return nil, err
	}

	// Step 9: Scheduled services run as Jobs, not as always-on Container Apps.
	if err := inferScheduledJobs(resources, app, env, getName, tags, identityID, managedServiceIdentityID, connections, connectionOrder); err != nil {
		return nil, err
	}

	// Step 10: Infer CDN for services with cdn_enabled.
	inferCdnAzure(resources, app, env, getName, tags)

	return resources, nil
}

// connectionOrderForAzure returns connection keys in insertion order:
// database connections first, then cache connections merged in, then
// storage connections merged in -- each built by iterating app.services in
// declaration order within its own capability filter -- producing every
// database-capability service (in declaration order), then every
// cache-capability service, then every object-storage-capability service,
// filtered to those with a connection.
func connectionOrderForAzure(app *models.Application, connections map[string]models.Connection) []string {
	order := make([]string, 0, len(connections))
	for _, capability := range []models.Capability{
		models.CapabilityDatabase,
		models.CapabilityCache,
		models.CapabilityObjectStorage,
	} {
		for i := range app.Services {
			name := app.Services[i].Name
			if app.Services[i].Capability != capability {
				continue
			}
			if _, ok := connections[name]; ok {
				order = append(order, name)
			}
		}
	}
	return order
}

// inferManagedIdentity creates or references a managed identity, mirroring
// _infer_managed_identity. Returns the identity resource ID for use in
// other resources. Creates nothing: a system-assigned identity is used
// implicitly elsewhere (see ContainerApp/ContainerAppJob's identity
// config) when no user-assigned one is configured.
func inferManagedIdentity(env *models.AzureEnvironment) string {
	if env.UserAssignedIdentityID != nil && *env.UserAssignedIdentityID != "" {
		return *env.UserAssignedIdentityID
	}
	return ""
}

// inferKeyVault creates the Key Vault for secrets, mirroring
// _infer_key_vault. Always creates exactly one, keyed "main".
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
	// authenticate when pushing (see handleBuildContextAzure and
	// generator_azure.go's registryAuth).
	registry.AdminEnabled = true
	registry.Tags = tags
	resources.ContainerRegistry["main"] = registry

	// Build and push an image for every service with a build context.
	// Mirrors AWS's handleBuildContext in compute_aws.go: a docker_image
	// builds locally, a docker_registry_image pushes it, and the
	// container's image reference is pinned to the pushed digest rather
	// than a mutable ":latest" tag, so Container Apps deploys exactly what
	// was built rather than racing a tag that could be overwritten
	// mid-apply.
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
