package azure

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for "generalize Azure's
// connection-string rendering" item: resolveEnvVarAzure substitutes a
// service's own authored environment: values against real managed-service
// connections, the same way aws/permissions.go's per-entry loop already
// did (both now built on shared.ResolveValue). This is additive to
// containerSpecAzure's own <SERVER>_URL synthesis, not a replacement --
// see resolveEnvVarAzure's own doc comment for why both coexist.

// dbAppAzure returns a minimal app with a single database-capability
// service named "db", the shape most resolveEnvVarAzure tests in this
// file need just to let redisTLSSchemeAzure's capability lookup resolve.
func dbAppAzure() *models.Application {
	return &models.Application{
		Services: []models.Service{{Name: "db", Capability: models.CapabilityDatabase}},
	}
}

func TestResolveEnvVarAzure_BareHostnameReferenceSubstituted(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	connections := map[string]models.Connection{
		"db": {Host: "${azurerm_postgresql_flexible_server.main.fqdn}", AddressedBy: "host"},
	}
	envVar, secret, _ := resolveEnvVarAzure(resources, dbAppAzure(), "web", "DATABASE_HOST", "db", connections, []string{"db"}, testGetNameAzure, nil, "")
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
	envVar, secret, _ := resolveEnvVarAzure(resources, dbAppAzure(), "web", "NODE_ENV", "development", connections, []string{"db"}, testGetNameAzure, nil, "")
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
	envVar, secret, _ := resolveEnvVarAzure(resources, dbAppAzure(), "web", "DATABASE_URL", "postgres://user@db:5432/localdb", connections, []string{"db"}, testGetNameAzure, nil, identityID)

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

	_, _, _ = resolveEnvVarAzure(resources, dbAppAzure(), "web", "DATABASE_URL", "postgres://db/x", connections, []string{"db"}, testGetNameAzure, nil, "${azurerm_user_assigned_identity.main.id}")

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
	_, secret, _ := resolveEnvVarAzure(resources, dbAppAzure(), "web", "DATABASE_URL", "postgres://db/x", connections, []string{"db"}, testGetNameAzure, nil, "${azurerm_user_assigned_identity.main.id}")
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

func TestResolveEnvVarAzure_ConfidentialWithNoIdentityIsAnError(t *testing.T) {
	t.Parallel()
	// No identity means Key Vault access can't be granted; a plaintext
	// fallback would leak the credential into Terraform state (#147).
	resources := models.NewAzureResources()
	password := "s3cret"
	connections := map[string]models.Connection{"db": {Host: "h", Password: &password}}
	_, secret, err := resolveEnvVarAzure(resources, dbAppAzure(), "web", "DATABASE_URL", "postgres://db/x", connections, []string{"db"}, testGetNameAzure, nil, "")
	if err == nil {
		t.Fatal("expected an error when a credential has no identity to read it from Key Vault")
	}
	if secret != nil {
		t.Errorf("expected no secret, got %+v", secret)
	}
}

// TestContainerSpecAzure_AuthoredEnvVarsAreSubstituted is the real
// integration test: containerSpecAzure wires resolveEnvVarAzure over
// service.Env, not just that the helper works in isolation. This is
// exactly the shape of the real bug found while doing this item --
// DATABASE_HOST: db shipping literally, unreachable once db is a
// managed Flexible Server -- confirmed against the doctor/edge-and-scaling
// golden fixtures too, this is the unit-level companion to those.
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
// described in item 3: a
// storage relationship used to render as a nonsensical Postgres-shaped
// URL ("postgresql://None:None@<host>:None/None"); it now renders as the
// bare host, matching the target's actual capability.
func TestContainerSpecAzure_ObjectStorageRendersAsBareHost(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, Env: map[string]string{"BUCKET": "blobs"}},
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
	var blobsURL *models.ContainerAppEnvVar
	for i := range container.Env {
		if container.Env[i].Name == "BLOBS_URL" {
			blobsURL = &container.Env[i]
		}
	}
	if blobsURL == nil {
		t.Fatalf("expected a BLOBS_URL env var, got %+v", container.Env)
	}
	want := "${azurerm_storage_account.blobs_storage.primary_blob_endpoint}"
	if blobsURL.Value != want {
		t.Errorf("got %q, want %q", blobsURL.Value, want)
	}
	if len(secrets) != 0 {
		t.Errorf("expected no secrets for a storage connection (no password), got %v", secrets)
	}
}

// TestContainerSpecAzure_CacheRendersAsRedisURL mirrors the same fix for
// a cache relationship: previously rendered as a Postgres-shaped URL,
// now renders as rediss:// (TLS -- Azure Managed Redis is TLS-only, see
// connectionURLAzure's own comment) with the cache's own host/port/password.
func TestConnectionURLAzure_CacheRendersAsRedisURL(t *testing.T) {
	t.Parallel()
	port := 10000
	password := "${azurerm_managed_redis.cache_redis.default_database[0].primary_access_key}"
	conn := models.Connection{
		Host:     "${azurerm_managed_redis.cache_redis.hostname}",
		Port:     &port,
		Password: &password,
	}
	want := "rediss://:" + password + "@${azurerm_managed_redis.cache_redis.hostname}:10000"
	if got := connectionURLAzure(models.CapabilityCache, &conn); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestContainerSpecAzure_DependsOnOnlyInjectsNothing is #147: a service
// that only depends_on: a managed service gets no identity or Key Vault
// grant (see referencedServersAzure), so it must not be handed that
// service's credentials either -- previously they shipped as plaintext.
func TestContainerSpecAzure_DependsOnOnlyInjectsNothing(t *testing.T) {
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
	password := "${random_password.postgres_admin.result}"
	connections := map[string]models.Connection{"db": {Host: "db.example.com", Password: &password}}

	container, secrets, err := containerSpecAzure(&app.Services[0], app, &env, models.NewAzureResources(), connections, []string{"db"}, testGetNameAzure, nil, "")
	if err != nil {
		t.Fatalf("containerSpecAzure failed: %v", err)
	}
	if len(container.Env) != 0 || len(secrets) != 0 {
		t.Errorf("expected no env vars or secrets for a depends_on:-only link, got env=%+v secrets=%+v", container.Env, secrets)
	}
}

// TestContainerSpecAzure_CredentialWithoutKeyVaultSecretIsAnError guards
// the fallback that used to write a password as a plaintext env var.
func TestContainerSpecAzure_CredentialWithoutKeyVaultSecretIsAnError(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, Env: map[string]string{"DB_HOST": "db"}},
			{Name: "db", Capability: models.CapabilityDatabase},
		},
	}
	env := mockAzureProdEnv()
	password := "pw"
	connections := map[string]models.Connection{"db": {Host: "db.example.com", Password: &password}}

	if _, _, err := containerSpecAzure(&app.Services[0], app, &env, models.NewAzureResources(), connections, []string{"db"}, testGetNameAzure, nil, ""); err == nil {
		t.Fatal("expected an error rather than a plaintext credential")
	}
}

// TestContainerSpecAzure_DatabaseUsesKeyVaultSecretRef confirms that once
// a KeyVaultSecret has been stored for a connection (as
// grantManagedServicePermissions does in the real pipeline), the
// container references it via SecretName rather than interpolating the
// password directly.
func TestContainerSpecAzure_DatabaseUsesKeyVaultSecretRef(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, Env: map[string]string{"DB_HOST": "db"}},
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
	// 2 env vars: DB_URL (the <SERVER>_URL synthesis under test here) plus
	// DB_PASSWORD, which containerSpecAzure now injects for every
	// referenced database connection alongside it (see
	// TestContainerSpecAzure_DatabaseConnectionInjectsUsernameAndPassword).
	// DB_HOST (authored), then DB_URL and DB_PASSWORD, which
	// containerSpecAzure injects for a referenced database connection (see
	// TestContainerSpecAzure_DatabaseConnectionInjectsUsernameAndPassword).
	if len(container.Env) != 3 {
		t.Fatalf("expected 3 env vars (DB_HOST, DB_URL, DB_PASSWORD), got %d: %+v", len(container.Env), container.Env)
	}
	if container.Env[1].Name != "DB_URL" || container.Env[1].Value != "" {
		t.Errorf("expected DB_URL with no plaintext Value when a Key Vault secret exists, got %+v", container.Env[1])
	}
	if container.Env[1].SecretName == "" {
		t.Errorf("expected SecretName to be set")
	}
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets (db-url, db-password), got %d", len(secrets))
	}
	if secrets[0].KeyVaultSecretID != "${azurerm_key_vault_secret.db_secret.versionless_id}" {
		t.Errorf("KeyVaultSecretID = %q, want the db_secret's versionless_id", secrets[0].KeyVaultSecretID)
	}
	if secrets[0].Identity != "${azurerm_user_assigned_identity.main.id}" {
		t.Errorf("Identity = %q, want the user-assigned identity's resource ID", secrets[0].Identity)
	}
}

// TestContainerSpecAzure_DatabaseConnectionInjectsUsernameAndPassword
// checks the real bug this closes: an app authoring DB_HOST itself
// (rather than consuming the synthesized <SERVER>_URL) still needs
// DB_USERNAME/DB_PASSWORD to actually connect, since the compose file
// never authors credentials for a container about to be replaced by a
// managed database. Mirrors AWS's grantDatabasePermissions, which
// injects the same two literal env var names from Secrets Manager.
func TestContainerSpecAzure_DatabaseConnectionInjectsUsernameAndPassword(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, Env: map[string]string{"DB_HOST": "db"}},
			{Name: "db", Capability: models.CapabilityDatabase},
		},
		Relationships: []models.Relationship{{Client: "web", Server: "db"}},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	resources.KeyVaultSecret["db_secret"] = models.NewKeyVaultSecret()
	resources.UserAssignedIdentity["main"] = models.UserAssignedIdentity{Name: "prod-app-identity"}

	password := "supersecret"
	username := "cloudcompose"
	connections := map[string]models.Connection{
		"db": {Host: "db.example.com", Username: &username, Password: &password},
	}

	container, secrets, err := containerSpecAzure(&app.Services[0], app, &env, resources, connections, []string{"db"}, testGetNameAzure, nil, "")
	if err != nil {
		t.Fatalf("containerSpecAzure failed: %v", err)
	}

	byName := map[string]models.ContainerAppEnvVar{}
	for _, e := range container.Env {
		byName[e.Name] = e
	}

	if byName["DB_USERNAME"].Value != "cloudcompose" {
		t.Errorf("DB_USERNAME = %+v, want plaintext value %q (a username is never confidential)", byName["DB_USERNAME"], "cloudcompose")
	}
	dbPassword, ok := byName["DB_PASSWORD"]
	if !ok {
		t.Fatalf("expected a DB_PASSWORD env var, got %+v", container.Env)
	}
	if dbPassword.Value != "" {
		t.Errorf("DB_PASSWORD.Value = %q, want empty (must come from Key Vault, not plaintext)", dbPassword.Value)
	}
	if dbPassword.SecretName == "" {
		t.Errorf("expected DB_PASSWORD.SecretName to be set")
	}

	var found bool
	for _, s := range secrets {
		if s.Name == dbPassword.SecretName {
			found = true
			if s.KeyVaultSecretID != "${azurerm_key_vault_secret.db_secret.versionless_id}" {
				t.Errorf("KeyVaultSecretID = %q, want the db_secret's versionless_id", s.KeyVaultSecretID)
			}
		}
	}
	if !found {
		t.Errorf("expected a ContainerAppSecret named %q, got %+v", dbPassword.SecretName, secrets)
	}
}

// TestInferAzure_RealServiceDiscoveryExample_PeerContainerIsReachable
// exercises the full pipeline against the real service-discovery example
// (#157). Two things must hold for web to reach api on Azure:
//   - API_URL is rewritten to api's Container App name, which is what the
//     environment's DNS resolves -- not the compose service name "api".
//   - api gets ingress, internal only: a Container App with no ingress is
//     unreachable even from inside its own environment.
//
// Pinned explicitly, not just via the golden byte-diff, since the previous
// literal pass-through was itself pinned as correct here.
func TestInferAzure_RealServiceDiscoveryExample_PeerContainerIsReachable(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/service-discovery/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "service-discovery")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := mockAzureProdEnv()
	resources, err := InferAzure(app, &env)
	if err != nil {
		t.Fatalf("InferAzure failed: %v", err)
	}

	webApp, ok := resources.ContainerApp["web"]
	if !ok {
		t.Fatalf("expected a container app for web, got keys %v", keysOf(resources.ContainerApp))
	}
	if len(webApp.Template.Container) != 1 {
		t.Fatalf("expected exactly 1 container in web's template, got %d", len(webApp.Template.Container))
	}

	var apiURL string
	found := false
	for _, e := range webApp.Template.Container[0].Env {
		if e.Name == "API_URL" {
			apiURL = e.Value
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an API_URL env var on web, got %+v", webApp.Template.Container[0].Env)
	}
	if apiURL != "http://prod-service-discovery-api" {
		t.Errorf("API_URL = %q, want api's Container App name (what the environment's DNS resolves)", apiURL)
	}
	api := resources.ContainerApp["api"]
	if api.Ingress == nil || api.Ingress.ExternalEnabled || api.Ingress.TargetPort != 80 {
		t.Errorf("api ingress = %+v, want internal-only ingress on port 80", api.Ingress)
	}

	if _, ok := resources.ContainerApp["api"]; !ok {
		t.Errorf("expected a container app for api too, got keys %v", keysOf(resources.ContainerApp))
	}
}

// TestInferAzure_RealDoctorExample_InjectsIdentityClientID: the doctor
// talks to Blob storage via DefaultAzureCredential, which only uses a
// user-assigned identity when AZURE_CLIENT_ID names it. Without it the
// SDK falls back to a system-assigned identity the app doesn't have.
func TestInferAzure_RealDoctorExample_InjectsIdentityClientID(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/doctor/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "doctor")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := mockAzureProdEnv()
	resources, err := InferAzure(app, &env)
	if err != nil {
		t.Fatalf("InferAzure failed: %v", err)
	}
	doctor := resources.ContainerApp["doctor"]
	if doctor.Identity == nil || doctor.Identity.Type != "UserAssigned" {
		t.Fatalf("doctor identity = %+v, want UserAssigned", doctor.Identity)
	}
	var got string
	for _, e := range doctor.Template.Container[0].Env {
		if e.Name == "AZURE_CLIENT_ID" {
			got = e.Value
		}
	}
	if want := "${azurerm_user_assigned_identity.main.client_id}"; got != want {
		t.Errorf("AZURE_CLIENT_ID = %q, want %q", got, want)
	}
}

// TestInjectIdentityClientIDAzure_OnlyForTheAppsOwnIdentity: no client
// ID for a service running as a system-assigned or environment-supplied
// identity, and an authored AZURE_CLIENT_ID is never overwritten.
func TestInjectIdentityClientIDAzure_OnlyForTheAppsOwnIdentity(t *testing.T) {
	t.Parallel()
	own := "${azurerm_user_assigned_identity.main.id}"
	for name, tc := range map[string]struct {
		assigned, managed string
		authored          bool
		want              string
	}{
		"own identity":         {own, own, false, "${azurerm_user_assigned_identity.main.client_id}"},
		"system-assigned":      {"", own, false, ""},
		"environment identity": {"/subscriptions/0/x", own, false, ""},
		"no app identity":      {"", "", false, ""},
		"authored stays":       {own, own, true, "mine"},
	} {
		c := models.ContainerAppContainer{}
		if tc.authored {
			c.Env = []models.ContainerAppEnvVar{{Name: "AZURE_CLIENT_ID", Value: "mine"}}
		}
		injectIdentityClientIDAzure(&c, tc.assigned, tc.managed)
		got, count := "", 0
		for _, e := range c.Env {
			if e.Name == "AZURE_CLIENT_ID" {
				got, count = e.Value, count+1
			}
		}
		if got != tc.want || count > 1 {
			t.Errorf("%s: AZURE_CLIENT_ID = %q (x%d), want %q", name, got, count, tc.want)
		}
	}
}
