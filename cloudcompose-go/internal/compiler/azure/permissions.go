package azure

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// keyVaultSecretsUserRole and storageBlobDataContributorRole are Azure's
// built-in RBAC role names for reading Key Vault secrets and read/write
// blob access respectively, scoped to a single Key Vault or storage
// account.
const (
	keyVaultSecretsUserRole        = "Key Vault Secrets User"
	storageBlobDataContributorRole = "Storage Blob Data Contributor"
)

// secretsPlaceholderValueAzure is the placeholder secret value for an
// operator to fill in out-of-band, with Azure-specific wording.
const secretsPlaceholderValueAzure = "PLACEHOLDER_VALUE_CHANGE_IN_AZURE_PORTAL"

// referencedServersAzure discovers, for every service in the app, which
// connections its authored `environment:` values actually resolve
// against. Computed once upfront: Azure's ordering constraint requires
// the identity to exist and be granted access before any Container
// App/Job that references it, so this must be known before
// containerSpecAzure runs.
func referencedServersAzure(app *models.Application, connections map[string]models.Connection) map[string]map[string]bool {
	referenced := map[string]map[string]bool{}
	order := shared.ConnectionOrder(app, connections)
	for i := range app.Services {
		service := &app.Services[i]
		for _, value := range service.Env {
			resolved := shared.ResolveValue(value, connections, order)
			if resolved.Service == nil {
				continue
			}
			if referenced[service.Name] == nil {
				referenced[service.Name] = map[string]bool{}
			}
			referenced[service.Name][*resolved.Service] = true
		}
	}
	return referenced
}

// inferManagedServiceIdentity creates one user-assigned identity per app
// that needs one: if any service references a database, cache, or
// object-storage connection, or declares compose secrets/platform
// config, so RoleAssignments granting access can be created before any
// Container App exists to use them.
//
// Must be user-assigned, not system-assigned: a system-assigned
// identity's principal_id doesn't exist until its owning resource is
// created, creating an ordering cycle with the role grant it needs at
// creation time. Returns "" (fall back to system-assigned) if no
// service needs one.
func inferManagedServiceIdentity(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
	connections map[string]models.Connection,
	referenced map[string]map[string]bool,
) string {
	needsIdentity := false
	for _, servers := range referenced {
		if len(servers) > 0 {
			needsIdentity = true
			break
		}
	}
	if !needsIdentity {
		for _, s := range app.Services {
			if len(s.Secrets) > 0 || len(s.Config) > 0 {
				needsIdentity = true
				break
			}
		}
	}
	if !needsIdentity {
		return ""
	}

	identity := models.UserAssignedIdentity{
		Name:              getName("identity"),
		ResourceGroupName: env.Name,
		Location:          env.Region,
		Tags:              tags,
	}
	resources.UserAssignedIdentity["main"] = identity

	return "${azurerm_user_assigned_identity.main.id}"
}

// principalIDRefForIdentity returns the Terraform expression for a
// UserAssignedIdentity's principal_id.
func principalIDRefForIdentity() string {
	return "${azurerm_user_assigned_identity.main.principal_id}"
}

// managedServiceIdentityRef returns the Terraform expression for the
// resource ID of the user-assigned identity inferManagedServiceIdentity
// created, or "" if none was created.
func managedServiceIdentityRef(resources *models.AzureResources) string {
	if _, ok := resources.UserAssignedIdentity["main"]; !ok {
		return ""
	}
	return "${azurerm_user_assigned_identity.main.id}"
}

// grantManagedServicePermissions creates Key Vault secrets for every
// database/cache connection's credential and a scoped RoleAssignment
// granting the app's user-assigned identity access to read them, plus
// Storage Blob Data Contributor for any object-storage connection any
// service references.
//
// The Key Vault role assignment is granted at most once per app: Azure's
// ARM API rejects duplicate RoleAssignments sharing the same
// (principal_id, role_definition_name, scope), and every credential
// this app stores shares the same vault, scope, and identity.
//
// Must run after inferManagedServiceIdentity and before containerSpecAzure
// builds any container that references these secrets by name.
func grantManagedServicePermissions(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
	identityID string,
	connections map[string]models.Connection,
	referenced map[string]map[string]bool,
) {
	if identityID == "" {
		return
	}
	principalIDRef := principalIDRefForIdentity()

	grantedStorage := map[string]bool{}
	grantedKeyVault := false

	// Every server any service references, deduplicated and sorted for
	// deterministic output.
	servers := map[string]bool{}
	for _, byService := range referenced {
		for server := range byService {
			servers[server] = true
		}
	}
	sortedServers := make([]string, 0, len(servers))
	for server := range servers {
		sortedServers = append(sortedServers, server)
	}
	sort.Strings(sortedServers)

	for _, server := range sortedServers {
		conn, ok := connections[server]
		if !ok {
			continue
		}

		if conn.Password != nil {
			storeManagedServiceSecret(resources, server, *conn.Password, getName, tags)
			grantKeyVaultAccessOnce(resources, &grantedKeyVault, principalIDRef)
			continue
		}

		// No password: this is an object-storage connection, granted via
		// RBAC directly rather than a credential value.
		if !grantedStorage[server] {
			grantedStorage[server] = true
			resources.RoleAssignment[server+"_storage_role"] = models.RoleAssignment{
				Scope:              fmt.Sprintf("${azurerm_storage_account.%s_storage.id}", server),
				RoleDefinitionName: storageBlobDataContributorRole,
				PrincipalID:        principalIDRef,
			}
		}
	}
}

// grantKeyVaultAccessOnce grants the app's identity Key Vault Secrets
// User on the shared Key Vault the first time it's called per app, and
// is a no-op afterward (Azure rejects a duplicate grant).
//
// Also creates the RBAC-propagation time_sleep alongside the role
// assignment, once per app.
func grantKeyVaultAccessOnce(resources *models.AzureResources, granted *bool, principalIDRef string) {
	if *granted {
		return
	}
	*granted = true
	resources.RoleAssignment["kv_role"] = models.RoleAssignment{
		Scope:              "${azurerm_key_vault.main.id}",
		RoleDefinitionName: keyVaultSecretsUserRole,
		PrincipalID:        principalIDRef,
	}
	resources.TimeSleep["kv_role_propagation"] = models.TimeSleep{
		CreateDuration: models.KeyVaultRoleAssignmentPropagationDelay,
		DependsOn:      []string{"azurerm_role_assignment.kv_role"},
	}
}

// storeManagedServiceSecret stores a database/cache credential in the
// app's Key Vault, keyed by the server service's name.
func storeManagedServiceSecret(
	resources *models.AzureResources,
	serverName, password string,
	getName func(string) string,
	tags map[string]string,
) {
	secret := models.NewKeyVaultSecret()
	secret.Name = getName(serverName + "-password")
	secret.KeyVaultID = "${azurerm_key_vault.main.id}"
	secret.Value = password
	resources.KeyVaultSecret[serverName+"_secret"] = secret
}

// keyVaultSecretRefFor returns the Terraform expression for the
// versionless secret ID stored for serverName, or "" if none was stored.
// versionless_id (not id) is used so a rotated secret's new value is
// picked up without redeploying the Container App that references it.
func keyVaultSecretRefFor(resources *models.AzureResources, serverName string) string {
	if _, ok := resources.KeyVaultSecret[serverName+"_secret"]; !ok {
		return ""
	}
	return fmt.Sprintf("${azurerm_key_vault_secret.%s_secret.versionless_id}", serverName)
}

// grantServiceSecretPermissions creates a Key Vault secret (placeholder
// value, for an operator to fill in out-of-band) for every compose
// `secrets:` entry a service declares, plus the RoleAssignment letting
// its identity read them. Returns the ContainerAppEnvVar/ContainerAppSecret
// pairs for containerSpecAzure to add to the container/app.
//
// identityID must be a managed-service identity's resource ID (see
// inferManagedServiceIdentity), created before this runs, since Container
// Apps resolves a Key Vault secret reference during the app's own
// creation.
func grantServiceSecretPermissions(
	resources *models.AzureResources,
	service *models.Service,
	app *models.Application,
	getName func(string) string,
	tags map[string]string,
	identityID string,
) ([]models.ContainerAppEnvVar, []models.ContainerAppSecret) {
	if len(service.Secrets) == 0 || identityID == "" {
		return nil, nil
	}

	var envVars []models.ContainerAppEnvVar
	var containerSecrets []models.ContainerAppSecret
	grantedKeyVault := false
	principalIDRef := principalIDRefForIdentity()

	for _, secretName := range service.Secrets {
		secretKey := fmt.Sprintf("%s_%s_secret", service.Name, secretName)

		secret := models.NewKeyVaultSecret()
		secret.Name = getName(fmt.Sprintf("%s-%s", service.Name, secretName))
		secret.KeyVaultID = "${azurerm_key_vault.main.id}"
		secret.Value = secretsPlaceholderValueAzure
		resources.KeyVaultSecret[secretKey] = secret

		containerSecretName := service.Name + "-" + secretName
		containerSecrets = append(containerSecrets, models.ContainerAppSecret{
			Name:             containerSecretName,
			KeyVaultSecretID: fmt.Sprintf("${azurerm_key_vault_secret.%s.versionless_id}", secretKey),
			Identity:         identityID,
		})

		envVarName := strings.ReplaceAll(strings.ToUpper(secretName), "-", "_")
		envVars = append(envVars, models.ContainerAppEnvVar{Name: envVarName, SecretName: containerSecretName})

		grantKeyVaultAccessOnce(resources, &grantedKeyVault, principalIDRef)
	}

	return envVars, containerSecrets
}

// grantPlatformConfigPermissions creates a Key Vault secret (placeholder
// value) per platform-supplied configuration key and the RoleAssignment
// letting the service's identity read them. Stores one Key Vault secret
// per key, since azurerm's key_vault_secret_id has no way to address a
// single key within a JSON blob.
func grantPlatformConfigPermissions(
	resources *models.AzureResources,
	service *models.Service,
	getName func(string) string,
	tags map[string]string,
	identityID string,
) ([]models.ContainerAppEnvVar, []models.ContainerAppSecret) {
	if len(service.Config) == 0 || identityID == "" {
		return nil, nil
	}

	var envVars []models.ContainerAppEnvVar
	var containerSecrets []models.ContainerAppSecret
	grantedKeyVault := false
	principalIDRef := principalIDRefForIdentity()

	for _, key := range service.Config {
		secretKey := fmt.Sprintf("%s_config_%s_secret", service.Name, strings.ToLower(key))

		secret := models.NewKeyVaultSecret()
		secret.Name = getName(fmt.Sprintf("%s-config-%s", service.Name, strings.ToLower(strings.ReplaceAll(key, "_", "-"))))
		secret.KeyVaultID = "${azurerm_key_vault.main.id}"
		secret.Value = secretsPlaceholderValueAzure
		resources.KeyVaultSecret[secretKey] = secret

		containerSecretName := strings.ToLower(strings.ReplaceAll(service.Name+"-config-"+key, "_", "-"))
		containerSecrets = append(containerSecrets, models.ContainerAppSecret{
			Name:             containerSecretName,
			KeyVaultSecretID: fmt.Sprintf("${azurerm_key_vault_secret.%s.versionless_id}", secretKey),
			Identity:         identityID,
		})

		envVars = append(envVars, models.ContainerAppEnvVar{Name: key, SecretName: containerSecretName})

		grantKeyVaultAccessOnce(resources, &grantedKeyVault, principalIDRef)
	}

	return envVars, containerSecrets
}
