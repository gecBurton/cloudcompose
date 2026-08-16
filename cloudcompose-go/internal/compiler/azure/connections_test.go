package azure

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for docs/azure-aws-parity-todo.md's "generalize Azure's
// connection-string rendering" item: resolveEnvVarAzure substitutes a
// service's own authored environment: values against real managed-service
// connections, the same way aws/permissions.go's per-entry loop already
// did (both now built on shared.ResolveValue). This is additive to
// containerSpecAzure's own <SERVER>_URL synthesis, not a replacement --
// see resolveEnvVarAzure's own doc comment for why both coexist.

func TestResolveEnvVarAzure_BareHostnameReferenceSubstituted(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	connections := map[string]models.Connection{
		"db": {Host: "${azurerm_postgresql_flexible_server.main.fqdn}", AddressedBy: "host"},
	}
	envVar, secret := resolveEnvVarAzure(resources, "web", "DATABASE_HOST", "db", connections, []string{"db"}, testGetNameAzure, nil, "")
	if envVar.Value != connections["db"].Host {
		t.Errorf("envVar.Value = %q, want %q (the real, deployed hostname, not the local container name)", envVar.Value, connections["db"].Host)
	}
	if secret != nil {
		t.Errorf("expected no Key Vault secret for a non-confidential value, got %+v", secret)
	}
}

func TestResolveEnvVarAzure_UnreferencedValuePassesThroughUnchanged(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	connections := map[string]models.Connection{"db": {Host: "h"}}
	envVar, secret := resolveEnvVarAzure(resources, "web", "NODE_ENV", "development", connections, []string{"db"}, testGetNameAzure, nil, "")
	if envVar.Value != "development" {
		t.Errorf("envVar.Value = %q, want unchanged 'development'", envVar.Value)
	}
	if secret != nil {
		t.Errorf("expected no secret, got %+v", secret)
	}
}

func TestResolveEnvVarAzure_ConfidentialURLStoredInKeyVault(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	resources.UserAssignedIdentity["main"] = models.UserAssignedIdentity{Name: "prod-app-identity"}
	password := "s3cret"
	connections := map[string]models.Connection{
		"db": {
			Host:     "${azurerm_postgresql_flexible_server.main.fqdn}",
			Port:     intPtr(5432),
			Username: strPtr("cloudcompose"),
			Password: &password,
			Database: strPtr("app_db"),
		},
	}
	identityID := "${azurerm_user_assigned_identity.main.id}"
	envVar, secret := resolveEnvVarAzure(resources, "web", "DATABASE_URL", "postgres://user@db:5432/localdb", connections, []string{"db"}, testGetNameAzure, nil, identityID)

	if envVar.Value != "" {
		t.Errorf("expected no plain Value on a confidential resolution, got %q", envVar.Value)
	}
	if envVar.SecretName == "" {
		t.Errorf("expected a SecretName on a confidential resolution")
	}
	if secret == nil {
		t.Fatalf("expected a ContainerAppSecret to be returned")
	}
	if secret.Identity != identityID {
		t.Errorf("secret.Identity = %q, want %q", secret.Identity, identityID)
	}

	stored, ok := resources.KeyVaultSecret["web_database_url_url"]
	if !ok {
		t.Fatalf("expected a KeyVaultSecret stored under web_database_url_url")
	}
	want := "postgres://cloudcompose:s3cret@${azurerm_postgresql_flexible_server.main.fqdn}:5432/app_db"
	if stored.Value != want {
		t.Errorf("stored secret value = %q, want %q", stored.Value, want)
	}
}

// TestResolveEnvVarAzure_GrantsKeyVaultAccessEvenWithoutARelationship
// checks a real gap that would otherwise exist: a service can reference
// a managed service by URL without also declaring depends_on: for it
// (schema-valid compose, if unusual). grantManagedServicePermissions's
// own Relationships-driven pass would never run for such a service, so
// without this function granting Key Vault access itself, the new
// secret it creates would have no RBAC role letting the identity read
// it -- a real (if narrow) bug, not just a theoretical one, that this
// test exists specifically to keep closed.
func TestResolveEnvVarAzure_GrantsKeyVaultAccessEvenWithoutARelationship(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	resources.UserAssignedIdentity["main"] = models.UserAssignedIdentity{Name: "prod-app-identity"}
	password := "s3cret"
	connections := map[string]models.Connection{"db": {Host: "h", Password: &password}}

	resolveEnvVarAzure(resources, "web", "DATABASE_URL", "postgres://db/x", connections, []string{"db"}, testGetNameAzure, nil, "${azurerm_user_assigned_identity.main.id}")

	if _, ok := resources.RoleAssignment["kv_role"]; !ok {
		t.Errorf("expected a kv_role RoleAssignment granting Key Vault access, got none")
	}
}

// TestResolveEnvVarAzure_KeyVaultSecretNameHasNoUnderscores checks the
// real bug found via terraform validate: azurerm_key_vault_secret's
// name may only contain alphanumeric characters and dashes -- an
// underscore-bearing env var name like DATABASE_URL must not leak an
// underscore into the secret's own Name field (the Terraform resource
// identifier/map key is a different matter and keeps underscores, same
// as every other secretKey in this file).
func TestResolveEnvVarAzure_KeyVaultSecretNameHasNoUnderscores(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	resources.UserAssignedIdentity["main"] = models.UserAssignedIdentity{Name: "prod-app-identity"}
	password := "s3cret"
	connections := map[string]models.Connection{
		"db": {Host: "h", Password: &password},
	}
	_, secret := resolveEnvVarAzure(resources, "web", "DATABASE_URL", "postgres://db/x", connections, []string{"db"}, testGetNameAzure, nil, "${azurerm_user_assigned_identity.main.id}")
	if secret == nil {
		t.Fatalf("expected a secret")
	}
	if strings.Contains(secret.Name, "_") {
		t.Errorf("ContainerAppSecret.Name = %q, must not contain underscores", secret.Name)
	}
	stored := resources.KeyVaultSecret["web_database_url_url"]
	if strings.Contains(stored.Name, "_") {
		t.Errorf("KeyVaultSecret.Name = %q, must not contain underscores", stored.Name)
	}
}

func TestResolveEnvVarAzure_ConfidentialWithNoIdentityFallsBackToPlainValue(t *testing.T) {
	t.Parallel()
	// No identity means Key Vault access can't be granted -- falling
	// back to a plain (unencrypted) value here is a real credential
	// leak into Terraform state, but the alternative (dropping the
	// value entirely) breaks the app outright; this mirrors
	// connectionURLAzure's own precedent of rendering something sane
	// rather than a value that can never resolve.
	resources := models.NewAzureResources()
	password := "s3cret"
	connections := map[string]models.Connection{"db": {Host: "h", Password: &password}}
	envVar, secret := resolveEnvVarAzure(resources, "web", "DATABASE_URL", "postgres://db/x", connections, []string{"db"}, testGetNameAzure, nil, "")
	if secret != nil {
		t.Errorf("expected no secret when no identity is available, got %+v", secret)
	}
	if envVar.Value == "" {
		t.Errorf("expected a plain fallback value when no identity is available")
	}
}

// TestContainerSpecAzure_AuthoredEnvVarsAreSubstituted is the real
// integration test: containerSpecAzure wires resolveEnvVarAzure over
// service.Env, not just that the helper works in isolation. This is
// exactly the shape of the real bug found while doing this item --
// DATABASE_HOST: db shipping literally, unreachable once db is a
// managed Flexible Server -- confirmed against the doctor/flask/
// nginx-flask-mysql/minio-s3/flask-s3/flask-redis golden fixtures too,
// this is the unit-level companion to those.
func TestContainerSpecAzure_AuthoredEnvVarsAreSubstituted(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{
				Name:       "web",
				Capability: models.CapabilityContainer,
				Env:        map[string]string{"DATABASE_HOST": "db", "NODE_ENV": "production"},
			},
			{Name: "db", Capability: models.CapabilityDatabase},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	connections := map[string]models.Connection{
		"db": {Host: "${azurerm_postgresql_flexible_server.main.fqdn}", AddressedBy: "host"},
	}

	container, _, err := containerSpecAzure(&app.Services[0], app, &env, resources, connections, []string{"db"}, testGetNameAzure, nil, "")
	if err != nil {
		t.Fatalf("containerSpecAzure failed: %v", err)
	}

	found := map[string]string{}
	for _, e := range container.Env {
		found[e.Name] = e.Value
	}
	if found["DATABASE_HOST"] != connections["db"].Host {
		t.Errorf("DATABASE_HOST = %q, want %q (substituted, not the literal compose value)", found["DATABASE_HOST"], connections["db"].Host)
	}
	if found["NODE_ENV"] != "production" {
		t.Errorf("NODE_ENV = %q, want unchanged 'production'", found["NODE_ENV"])
	}
}

// TestContainerSpecAzure_ObjectStorageRendersAsBareHost mirrors the fix
// described in docs/azure-aws-parity-todo.md Priority 1 item 3: a
// storage relationship used to render as a nonsensical Postgres-shaped
// URL ("postgresql://None:None@<host>:None/None"); it now renders as the
// bare host, matching the target's actual capability.
func TestContainerSpecAzure_ObjectStorageRendersAsBareHost(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer},
			{Name: "blobs", Capability: models.CapabilityObjectStorage},
		},
		Relationships: []models.Relationship{{Client: "web", Server: "blobs"}},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	name := "${azurerm_storage_account.blobs_storage.name}"
	connections := map[string]models.Connection{
		"blobs": {
			Host:        "${azurerm_storage_account.blobs_storage.primary_blob_endpoint}",
			Name:        &name,
			AddressedBy: "name",
		},
	}

	container, secrets, err := containerSpecAzure(&app.Services[0], app, &env, resources, connections, []string{"blobs"}, testGetNameAzure, nil, "")
	if err != nil {
		t.Fatalf("containerSpecAzure failed: %v", err)
	}
	if len(container.Env) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(container.Env))
	}
	want := "${azurerm_storage_account.blobs_storage.primary_blob_endpoint}"
	if container.Env[0].Value != want {
		t.Errorf("got %q, want %q", container.Env[0].Value, want)
	}
	if len(secrets) != 0 {
		t.Errorf("expected no secrets for a storage connection (no password), got %v", secrets)
	}
}

// TestContainerSpecAzure_CacheRendersAsRedisURL mirrors the same fix for
// a cache relationship: previously rendered as a Postgres-shaped URL,
// now renders as redis:// with the cache's own host/port/password.
func TestContainerSpecAzure_CacheRendersAsRedisURL(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer},
			{Name: "cache", Capability: models.CapabilityCache},
		},
		Relationships: []models.Relationship{{Client: "web", Server: "cache"}},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	port := 10000
	password := "${azurerm_managed_redis.cache_redis.default_database[0].primary_access_key}"
	connections := map[string]models.Connection{
		"cache": {
			Host:     "${azurerm_managed_redis.cache_redis.hostname}",
			Port:     &port,
			Password: &password,
		},
	}

	container, secrets, err := containerSpecAzure(&app.Services[0], app, &env, resources, connections, []string{"cache"}, testGetNameAzure, nil, "")
	if err != nil {
		t.Fatalf("containerSpecAzure failed: %v", err)
	}
	if len(container.Env) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(container.Env))
	}
	// No KeyVaultSecret was stored for "cache" (that's
	// grantManagedServicePermissions's job, not containerSpecAzure's),
	// so this exercises the plain-render fallback path, not the
	// secretRef path -- see TestContainerSpecAzure_DatabaseUsesKeyVaultSecretRef
	// for that one.
	want := "redis://:" + password + "@${azurerm_managed_redis.cache_redis.hostname}:10000"
	if container.Env[0].Value != want {
		t.Errorf("got %q, want %q", container.Env[0].Value, want)
	}
	if len(secrets) != 0 {
		t.Errorf("expected no secrets when none was stored in Key Vault, got %v", secrets)
	}
}

// TestContainerSpecAzure_DatabaseUsesKeyVaultSecretRef confirms that once
// a KeyVaultSecret has been stored for a connection (as
// grantManagedServicePermissions does in the real pipeline), the
// container references it via SecretName rather than interpolating the
// password directly -- see docs/azure-aws-parity-todo.md Priority 1
// items 1-2.
func TestContainerSpecAzure_DatabaseUsesKeyVaultSecretRef(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer},
			{Name: "db", Capability: models.CapabilityDatabase},
		},
		Relationships: []models.Relationship{{Client: "web", Server: "db"}},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	resources.KeyVaultSecret["db_secret"] = models.NewKeyVaultSecret()
	resources.UserAssignedIdentity["main"] = models.UserAssignedIdentity{Name: "prod-app-identity"}

	password := "supersecret"
	connections := map[string]models.Connection{
		"db": {Host: "db.example.com", Password: &password},
	}

	container, secrets, err := containerSpecAzure(&app.Services[0], app, &env, resources, connections, []string{"db"}, testGetNameAzure, nil, "")
	if err != nil {
		t.Fatalf("containerSpecAzure failed: %v", err)
	}
	if len(container.Env) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(container.Env))
	}
	if container.Env[0].Value != "" {
		t.Errorf("expected no plaintext Value when a Key Vault secret exists, got %q", container.Env[0].Value)
	}
	if container.Env[0].SecretName == "" {
		t.Errorf("expected SecretName to be set")
	}
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if secrets[0].KeyVaultSecretID != "${azurerm_key_vault_secret.db_secret.versionless_id}" {
		t.Errorf("KeyVaultSecretID = %q, want the db_secret's versionless_id", secrets[0].KeyVaultSecretID)
	}
	if secrets[0].Identity != "${azurerm_user_assigned_identity.main.id}" {
		t.Errorf("Identity = %q, want the user-assigned identity's resource ID", secrets[0].Identity)
	}
}
