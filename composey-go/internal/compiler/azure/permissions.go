package azure

import (
	"fmt"
	"strings"

	"github.com/gecburton/composey/internal/models"
)

// keyVaultSecretsUserRole and storageBlobDataContributorRole are Azure's
// built-in RBAC role names for reading Key Vault secrets and read/write
// blob access respectively -- the roles this package grants scoped to a
// single Key Vault or storage account, mirroring aws/permissions.go's
// scoped IamRolePolicy attachments (never account-wide).
const (
	keyVaultSecretsUserRole        = "Key Vault Secrets User"
	storageBlobDataContributorRole = "Storage Blob Data Contributor"
)

// secretsPlaceholderValueAzure mirrors shared.SecretsPlaceholderValue,
// but with wording that's actually correct for this cloud: the shared
// constant's own text says "CHANGE IN AWS CONSOLE", which would be wrong
// (and confusing) advice for an Azure operator, who'd need to change it
// in the Azure Portal/CLI against Key Vault, not AWS Secrets Manager.
// Kept local to this package rather than added to shared/constants.go,
// since the wording is inherently cloud-specific, not something to
// centralize.
const secretsPlaceholderValueAzure = "PLACEHOLDER_VALUE_CHANGE_IN_AZURE_PORTAL"

// inferManagedServiceIdentity creates one user-assigned identity per app
// that needs one -- i.e. if any service has a Relationship to a database,
// cache, or object-storage service, or declares compose secrets:/has
// platform-supplied config (see grantServiceSecretPermissions/
// grantPlatformConfigPermissions) -- so RoleAssignments granting it
// access to Key Vault secrets/storage can be created *before* any
// Container App exists to use them.
//
// This must be user-assigned, not system-assigned (Container Apps'
// default): a system-assigned identity's principal_id doesn't exist
// until the resource that owns it is created, so it can't be granted a
// role before that resource's own creation -- but that resource's
// creation is exactly when the role is needed, to resolve a Key Vault
// secret reference or reach a storage account. A standalone
// UserAssignedIdentity resource has no such ordering cycle: it can be
// created and granted roles first, then attached to every Container
// App/Job that needs them. Returns "" (meaning: fall back to
// system-assigned, via managedIdentityAzure) if no service has any such
// need, so apps with no managed-service credentials don't pay for
// an identity they'd never use.
func inferManagedServiceIdentity(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
	connections map[string]models.Connection,
) string {
	needsIdentity := false
	for _, r := range app.Relationships {
		if _, ok := connections[r.Server]; ok {
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
// UserAssignedIdentity's principal_id, given the same resource-ID
// expression inferManagedServiceIdentity returned.
func principalIDRefForIdentity() string {
	return "${azurerm_user_assigned_identity.main.principal_id}"
}

// managedServiceIdentityRef returns the Terraform expression for the
// resource ID of the user-assigned identity inferManagedServiceIdentity
// created, or "" if none was created (no service needed one). This is
// what a ContainerAppSecret's own Identity field expects -- the
// identity's resource ID, not its principal_id (that's what
// RoleAssignment.PrincipalID wants instead, via principalIDRefForIdentity).
func managedServiceIdentityRef(resources *models.AzureResources) string {
	if _, ok := resources.UserAssignedIdentity["main"]; !ok {
		return ""
	}
	return "${azurerm_user_assigned_identity.main.id}"
}

// grantManagedServicePermissions creates Key Vault secrets for every
// database/cache connection's credential and a scoped RoleAssignment
// granting the app's user-assigned identity access to read them, plus
// Storage Blob Data Contributor for any object-storage relationship,
// mirroring aws/permissions.go's grantDatabasePermissions/
// grantS3Permissions -- but as RBAC role assignments rather than IAM
// policies, since that's Azure's equivalent primitive.
//
// The Key Vault role assignment is granted at most once per app, no
// matter how many credentials end up stored in it: Azure's ARM API
// rejects two RoleAssignments with an identical
// (principal_id, role_definition_name, scope) triple as a duplicate, and
// every credential this function stores shares the same Key Vault (and
// therefore the same scope) and the same identity -- so a DB secret and
// a cache secret each granting their own "Key Vault Secrets User" on the
// same vault would collide. Confirmed as a real bug, not a hypothetical
// one, while implementing secrets:/config: support in the same file
// (2026-08-08): the doctor/production-stack golden fixtures already had
// exactly this duplicate before this fix.
//
// Must run after inferManagedServiceIdentity (needs the identity to grant
// roles to) and before containerSpecAzure builds any container that
// references these secrets by name.
func grantManagedServicePermissions(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
	identityID string,
	connections map[string]models.Connection,
) {
	if identityID == "" {
		return
	}
	principalIDRef := principalIDRefForIdentity()

	grantedStorage := map[string]bool{}
	grantedKeyVault := false

	for _, r := range app.Relationships {
		conn, ok := connections[r.Server]
		if !ok {
			continue
		}

		if conn.Password != nil {
			storeManagedServiceSecret(resources, r.Server, *conn.Password, getName, tags)
			grantKeyVaultAccessOnce(resources, &grantedKeyVault, principalIDRef)
			continue
		}

		// No password: this is an object-storage connection (see
		// models.Connection's own doc comment -- storage access is
		// granted via RBAC directly, not a credential value, so there's
		// no secret to store).
		if !grantedStorage[r.Server] {
			grantedStorage[r.Server] = true
			resources.RoleAssignment[r.Server+"_storage_role"] = models.RoleAssignment{
				Scope:              fmt.Sprintf("${azurerm_storage_account.%s_storage.id}", r.Server),
				RoleDefinitionName: storageBlobDataContributorRole,
				PrincipalID:        principalIDRef,
			}
		}
	}
}

// grantKeyVaultAccessOnce grants the app's identity Key Vault Secrets
// User on the shared Key Vault the first time it's called (per app), and
// is a no-op on every subsequent call -- see grantManagedServicePermissions's
// own doc comment for why a second identical grant would be a duplicate
// Azure rejects, not just redundant Terraform config.
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
}

// storeManagedServiceSecret stores a database/cache credential in the
// app's Key Vault, keyed by the server service's name so
// keyVaultSecretRefFor can look it up again when wiring a container's
// env vars.
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
// versionless secret ID stored for serverName, or "" if none was stored
// (e.g. no service has a Relationship to it, or it's a storage
// connection with no credential). versionless_id (not id) is used
// deliberately: a rotated secret's new value is picked up without
// redeploying the Container App that references it.
func keyVaultSecretRefFor(resources *models.AzureResources, serverName string) string {
	if _, ok := resources.KeyVaultSecret[serverName+"_secret"]; !ok {
		return ""
	}
	return fmt.Sprintf("${azurerm_key_vault_secret.%s_secret.versionless_id}", serverName)
}

// grantServiceSecretPermissions creates a Key Vault secret (placeholder
// value, for an operator to fill in out-of-band) for every compose
// `secrets:` entry a service declares, plus the RoleAssignment letting
// its identity read them -- mirroring aws/compute.go's handleSecrets,
// but through Key Vault + RBAC instead of Secrets Manager + IAM.
//
// Returns the ContainerAppEnvVar/ContainerAppSecret pairs for the
// caller (containerSpecAzure) to add to the container/app respectively;
// this package's functions build resources, but the container spec
// itself is compute.go's responsibility, the same split
// grantManagedServicePermissions/containerSpecAzure already use.
//
// identityID must be a *managed-service* identity's resource ID (see
// inferManagedServiceIdentity), created before this runs, for the same
// ordering reason documented there: Container Apps resolves a Key Vault
// secret reference as part of the app's own creation, so the identity
// needs its role granted first.
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
// value) per platform-supplied configuration key
// (`x-cloud`-inferred, valued outside the compose file -- see
// service.Config's own doc comment in models/semantic.go) and the
// RoleAssignment letting the service's identity read them, mirroring
// aws/compute.go's handlePlatformConfig. Unlike AWS (which packs every
// config key into one Secrets Manager secret and slices it by JSON key
// at read time via `arn:...:key::`), this stores one Key Vault secret
// per key: azurerm's key_vault_secret_id has no equivalent "read one key
// out of a JSON blob" addressing, so one secret per key is the natural
// shape here instead.
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
