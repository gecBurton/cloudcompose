package azure

import (
	"fmt"

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

// inferManagedServiceIdentity creates one user-assigned identity per app
// that needs one -- i.e. if any service has a Relationship to a database,
// cache, or object-storage service -- so RoleAssignments granting it
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
// relationship, so apps with no managed-service credentials don't pay for
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
	grantedSecret := map[string]bool{}

	for _, r := range app.Relationships {
		conn, ok := connections[r.Server]
		if !ok {
			continue
		}

		if conn.Password != nil {
			if !grantedSecret[r.Server] {
				grantedSecret[r.Server] = true
				storeManagedServiceSecret(resources, r.Server, *conn.Password, getName, tags)
				resources.RoleAssignment[r.Server+"_kv_role"] = models.RoleAssignment{
					Scope:              "${azurerm_key_vault.main.id}",
					RoleDefinitionName: keyVaultSecretsUserRole,
					PrincipalID:        principalIDRef,
				}
			}
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
