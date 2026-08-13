package azure

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for docs/azure-aws-parity-todo.md's Priority 2 items: compose
// secrets:, platform config:, database sizing, MariaDB detection, and
// CPU/Memory autoscaling -- all previously silent no-ops on Azure.

func TestGrantServiceSecretPermissions_CreatesKeyVaultSecretAndRoleAssignment(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web", Secrets: []string{"db-password"}}
	app := &models.Application{Name: "myapp", Services: []models.Service{*service}}
	resources := models.NewAzureResources()
	identityID := "${azurerm_user_assigned_identity.main.id}"
	resources.UserAssignedIdentity["main"] = models.UserAssignedIdentity{Name: "prod-app-identity"}

	envVars, secrets := grantServiceSecretPermissions(resources, service, app, minimalGetName("prod", "myapp"), nil, identityID)

	if len(envVars) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(envVars))
	}
	if envVars[0].Name != "DB_PASSWORD" {
		t.Errorf("env var name = %q, want DB_PASSWORD", envVars[0].Name)
	}
	if envVars[0].SecretName == "" {
		t.Errorf("expected a SecretName, got none")
	}
	if len(secrets) != 1 {
		t.Fatalf("expected 1 container secret, got %d", len(secrets))
	}
	if secrets[0].Identity != identityID {
		t.Errorf("secret identity = %q, want %q", secrets[0].Identity, identityID)
	}

	if len(resources.KeyVaultSecret) != 1 {
		t.Fatalf("expected 1 KeyVaultSecret resource, got %d", len(resources.KeyVaultSecret))
	}
	for _, secret := range resources.KeyVaultSecret {
		if secret.Value != secretsPlaceholderValueAzure {
			t.Errorf("secret value = %q, want the placeholder (never the real value)", secret.Value)
		}
	}

	if len(resources.RoleAssignment) != 1 {
		t.Fatalf("expected 1 RoleAssignment, got %d", len(resources.RoleAssignment))
	}
	for _, ra := range resources.RoleAssignment {
		if ra.RoleDefinitionName != keyVaultSecretsUserRole {
			t.Errorf("role = %q, want %q", ra.RoleDefinitionName, keyVaultSecretsUserRole)
		}
	}
}

func TestGrantServiceSecretPermissions_NoSecretsIsNoop(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web"}
	app := &models.Application{Name: "myapp", Services: []models.Service{*service}}
	resources := models.NewAzureResources()

	envVars, secrets := grantServiceSecretPermissions(resources, service, app, minimalGetName("prod", "myapp"), nil, "${azurerm_user_assigned_identity.main.id}")
	if envVars != nil || secrets != nil {
		t.Errorf("expected no env vars or secrets for a service with no compose secrets, got %v, %v", envVars, secrets)
	}
}

func TestGrantPlatformConfigPermissions_CreatesKeyVaultSecretPerKey(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web", Config: []string{"API_TOKEN", "SENTRY_DSN"}}
	resources := models.NewAzureResources()
	identityID := "${azurerm_user_assigned_identity.main.id}"

	envVars, secrets := grantPlatformConfigPermissions(resources, service, minimalGetName("prod", "myapp"), nil, identityID)

	if len(envVars) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(envVars))
	}
	gotNames := map[string]bool{envVars[0].Name: true, envVars[1].Name: true}
	if !gotNames["API_TOKEN"] || !gotNames["SENTRY_DSN"] {
		t.Errorf("expected env var names API_TOKEN and SENTRY_DSN, got %v", gotNames)
	}
	if len(secrets) != 2 {
		t.Fatalf("expected 2 container secrets, got %d", len(secrets))
	}
	if len(resources.KeyVaultSecret) != 2 {
		t.Fatalf("expected 2 KeyVaultSecret resources (one per config key), got %d", len(resources.KeyVaultSecret))
	}
	// Only one Key Vault role assignment, even though two secrets were
	// created -- see grantKeyVaultAccessOnce's own doc comment for why a
	// second identical grant would be an Azure-rejected duplicate, not
	// just redundant.
	if len(resources.RoleAssignment) != 1 {
		t.Errorf("expected exactly 1 RoleAssignment (deduplicated), got %d", len(resources.RoleAssignment))
	}
}

func TestGrantPlatformConfigPermissions_NoConfigIsNoop(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web"}
	resources := models.NewAzureResources()

	envVars, secrets := grantPlatformConfigPermissions(resources, service, minimalGetName("prod", "myapp"), nil, "${azurerm_user_assigned_identity.main.id}")
	if envVars != nil || secrets != nil {
		t.Errorf("expected no env vars or secrets for a service with no platform config, got %v, %v", envVars, secrets)
	}
}

func TestGrantManagedServicePermissions_DoesNotDuplicateKeyVaultRoleAssignment(t *testing.T) {
	t.Parallel()
	// Regression test: an app with both a database and a cache
	// relationship used to get two RoleAssignment resources granting
	// the identical (principal_id, role_definition_name, scope) triple,
	// which Azure's ARM API rejects as a duplicate.
	app := &models.Application{
		Name: "myapp",
		Relationships: []models.Relationship{
			{Client: "web", Server: "db"},
			{Client: "web", Server: "cache"},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	dbPassword := "dbpass"
	cachePassword := "cachepass"
	connections := map[string]models.Connection{
		"db":    {Host: "db.example.com", Password: &dbPassword},
		"cache": {Host: "cache.example.com", Password: &cachePassword},
	}

	grantManagedServicePermissions(resources, app, &env, minimalGetName("prod", "myapp"), nil, "${azurerm_user_assigned_identity.main.id}", connections,
		map[string]map[string]bool{"web": {"db": true, "cache": true}})

	kvRoleAssignments := 0
	for _, ra := range resources.RoleAssignment {
		if ra.RoleDefinitionName == keyVaultSecretsUserRole {
			kvRoleAssignments++
		}
	}
	if kvRoleAssignments != 1 {
		t.Errorf("expected exactly 1 Key Vault Secrets User RoleAssignment for 2 credential-bearing connections sharing 1 Key Vault, got %d", kvRoleAssignments)
	}
	if len(resources.KeyVaultSecret) != 2 {
		t.Errorf("expected 2 KeyVaultSecret resources (one per connection), got %d", len(resources.KeyVaultSecret))
	}
}

// Tests for docs/azure-todo.md's "Key Vault role-assignment RBAC
// propagation" item: azurerm_role_assignment.kv_role reporting created
// does not mean the grant has actually propagated on Azure's side
// --
// grantKeyVaultAccessOnce now also creates a time_sleep every
// azurerm_key_vault_secret depends on.

func TestGrantKeyVaultAccessOnce_CreatesPropagationSleep(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	granted := false
	grantKeyVaultAccessOnce(resources, &granted, principalIDRefForIdentity())

	sleep, ok := resources.TimeSleep["kv_role_propagation"]
	if !ok {
		t.Fatalf("expected a kv_role_propagation TimeSleep resource")
	}
	if sleep.CreateDuration != models.KeyVaultRoleAssignmentPropagationDelay {
		t.Errorf("CreateDuration = %q, want %q", sleep.CreateDuration, models.KeyVaultRoleAssignmentPropagationDelay)
	}
	if len(sleep.DependsOn) != 1 || sleep.DependsOn[0] != "azurerm_role_assignment.kv_role" {
		t.Errorf("DependsOn = %v, want [azurerm_role_assignment.kv_role]", sleep.DependsOn)
	}
}

// TestGrantKeyVaultAccessOnce_SleepCreatedExactlyOnce is the TimeSleep
// counterpart to TestGrantManagedServicePermissions_DoesNotDuplicateKeyVaultRoleAssignment:
// the sleep is keyed the same way the role assignment is (one per app,
// not one per credential), so a second call must not create a second,
// differently-keyed sleep resource that other secrets might miss
// depending on.
func TestGrantKeyVaultAccessOnce_SleepCreatedExactlyOnce(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	granted := false
	grantKeyVaultAccessOnce(resources, &granted, principalIDRefForIdentity())
	grantKeyVaultAccessOnce(resources, &granted, principalIDRefForIdentity())

	if len(resources.TimeSleep) != 1 {
		t.Errorf("expected exactly 1 TimeSleep resource after two calls, got %d", len(resources.TimeSleep))
	}
}

// TestNewKeyVaultSecret_DependsOnPropagationSleep checks the fix at its
// actual source: every KeyVaultSecret this codebase creates goes
// through this one constructor (confirmed by grep across
// internal/compiler/azure -- see the constructor's own doc comment), so
// fixing it here, rather than at each of the 4 call sites individually,
// is what makes the fix apply everywhere without relying on every
// future call site remembering to set it.
func TestNewKeyVaultSecret_DependsOnPropagationSleep(t *testing.T) {
	t.Parallel()
	secret := models.NewKeyVaultSecret()
	if len(secret.DependsOn) != 1 || secret.DependsOn[0] != "time_sleep.kv_role_propagation" {
		t.Errorf("DependsOn = %v, want [time_sleep.kv_role_propagation]", secret.DependsOn)
	}
}

// TestGenerateAzure_TimeProviderOnlyDeclaredWhenNeeded checks the same
// "don't declare a provider you have no resource of" convention already
// used for the docker provider (see GenerateAzure's own comment): an app
// with no managed-service credentials never creates a Key Vault at all,
// and shouldn't pull in the time provider either.
func TestGenerateAzure_TimeProviderOnlyDeclaredWhenNeeded(t *testing.T) {
	t.Parallel()
	env := mockAzureProdEnv()

	withoutSleep := models.NewAzureResources()
	out, err := GenerateAzure(withoutSleep, &env)
	if err != nil {
		t.Fatalf("GenerateAzure failed: %v", err)
	}
	if strings.Contains(out, `"time":`) {
		t.Errorf("expected no time provider declared when nothing needs it, got:\n%s", out)
	}

	withSleep := models.NewAzureResources()
	granted := false
	grantKeyVaultAccessOnce(withSleep, &granted, principalIDRefForIdentity())
	out, err = GenerateAzure(withSleep, &env)
	if err != nil {
		t.Fatalf("GenerateAzure failed: %v", err)
	}
	if !strings.Contains(out, `"hashicorp/time"`) {
		t.Errorf("expected the time provider to be declared when a TimeSleep resource exists, got:\n%s", out)
	}
}

// Tests for docs/azure-aws-parity-todo.md's "Azure's RBAC/identity-granting
// model is depends_on:-driven, where AWS's is usage-driven" item.
// referencedServersAzure/inferManagedServiceIdentity/
// grantManagedServicePermissions/identityForService now all grant based on
// what a service's own authored environment: values actually reference
// (via shared.ResolveValue), not on app.Relationships (compose
// depends_on:) directly -- matching aws/permissions.go's own
// referencedNames model exactly.

func TestReferencedServersAzure_FindsBareReferenceAndURLUsage(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Env: map[string]string{"DATABASE_URL": "postgres://db:5432/app", "BUCKET_NAME": "blobs"}},
			{Name: "db", Capability: models.CapabilityDatabase},
			{Name: "blobs", Capability: models.CapabilityObjectStorage},
		},
	}
	connections := map[string]models.Connection{
		"db":    {Host: "db.example.com"},
		"blobs": {Host: "blobs.example.com", AddressedBy: "name", Name: strPtr("real-bucket")},
	}
	referenced := referencedServersAzure(app, connections)

	if !referenced["web"]["db"] {
		t.Errorf("expected web to reference db (via DATABASE_URL), got %v", referenced["web"])
	}
	if !referenced["web"]["blobs"] {
		t.Errorf("expected web to reference blobs (via bare BUCKET_NAME reference), got %v", referenced["web"])
	}
}

func TestReferencedServersAzure_IgnoresDependsOnWithNoActualReference(t *testing.T) {
	t.Parallel()
	// The exact scenario the tracker item described: a service
	// depends_on: db for pure startup-ordering reasons, but never
	// references it in any env var.
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Env: map[string]string{"NODE_ENV": "production"}},
			{Name: "db"},
		},
		Relationships: []models.Relationship{{Client: "web", Server: "db"}},
	}
	connections := map[string]models.Connection{"db": {Host: "db.example.com"}}
	referenced := referencedServersAzure(app, connections)

	if len(referenced["web"]) != 0 {
		t.Errorf("expected web to reference nothing (depends_on: db is not env-var usage), got %v", referenced["web"])
	}
}

func TestInferManagedServiceIdentity_NoIdentityWhenDependsOnButUnreferenced(t *testing.T) {
	t.Parallel()
	// The real end-to-end bug this item found: before this fix, a
	// service that depends_on: a managed service but never references
	// it in an env var still got a user-assigned identity and Key Vault
	// role grant it never needed -- an over-grant AWS's own
	// usage-driven model would correctly withhold.
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Env: map[string]string{"NODE_ENV": "production"}},
			{Name: "db", Capability: models.CapabilityDatabase},
		},
		Relationships: []models.Relationship{{Client: "web", Server: "db"}},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	connections := map[string]models.Connection{"db": {Host: "db.example.com", Password: strPtr("pw")}}
	referenced := referencedServersAzure(app, connections)

	identityID := inferManagedServiceIdentity(resources, app, &env, testGetNameAzure, nil, connections, referenced)
	if identityID != "" {
		t.Errorf("expected no managed-service identity for a depends_on: with no actual env-var reference, got %q", identityID)
	}
	if _, ok := resources.UserAssignedIdentity["main"]; ok {
		t.Errorf("expected no UserAssignedIdentity to be created")
	}
}

func TestInferManagedServiceIdentity_IdentityCreatedWhenActuallyReferenced(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Env: map[string]string{"DATABASE_URL": "postgres://db:5432/app"}},
			{Name: "db", Capability: models.CapabilityDatabase},
		},
		Relationships: []models.Relationship{{Client: "web", Server: "db"}},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	connections := map[string]models.Connection{"db": {Host: "db.example.com", Password: strPtr("pw")}}
	referenced := referencedServersAzure(app, connections)

	identityID := inferManagedServiceIdentity(resources, app, &env, testGetNameAzure, nil, connections, referenced)
	if identityID == "" {
		t.Errorf("expected a managed-service identity when web actually references db")
	}
	if _, ok := resources.UserAssignedIdentity["main"]; !ok {
		t.Errorf("expected a UserAssignedIdentity to be created")
	}
}

func TestGrantManagedServicePermissions_OnlyGrantsForActuallyReferencedServers(t *testing.T) {
	t.Parallel()
	// web depends_on: both db and cache, but only references db in an
	// env var. Only db's credential should be stored/granted.
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Env: map[string]string{"DATABASE_URL": "postgres://db:5432/app"}},
			{Name: "db", Capability: models.CapabilityDatabase},
			{Name: "cache", Capability: models.CapabilityCache},
		},
		Relationships: []models.Relationship{
			{Client: "web", Server: "db"},
			{Client: "web", Server: "cache"},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	connections := map[string]models.Connection{
		"db":    {Host: "db.example.com", Password: strPtr("dbpw")},
		"cache": {Host: "cache.example.com", Password: strPtr("cachepw")},
	}
	referenced := referencedServersAzure(app, connections)
	identityID := inferManagedServiceIdentity(resources, app, &env, testGetNameAzure, nil, connections, referenced)
	grantManagedServicePermissions(resources, app, &env, testGetNameAzure, nil, identityID, connections, referenced)

	if _, ok := resources.KeyVaultSecret["db_secret"]; !ok {
		t.Errorf("expected a db_secret secret (db is actually referenced)")
	}
	if _, ok := resources.KeyVaultSecret["cache_secret"]; ok {
		t.Errorf("expected no cache_secret secret (cache is depends_on: only, never referenced)")
	}
}

func TestIdentityForService_UsesManagedIdentityOnlyWhenServiceItselfReferences(t *testing.T) {
	t.Parallel()
	// Two services in one app: "web" references db, "worker" depends_on:
	// db (in Relationships) but has no env var referencing it at all.
	// Only web should get the managed-service identity.
	referenced := map[string]map[string]bool{"web": {"db": true}}

	webIdentity := identityForService(&models.Service{Name: "web"}, "", "managed-id", referenced)
	if webIdentity != "managed-id" {
		t.Errorf("web identity = %q, want managed-id", webIdentity)
	}

	workerIdentity := identityForService(&models.Service{Name: "worker"}, "fallback-id", "managed-id", referenced)
	if workerIdentity != "fallback-id" {
		t.Errorf("worker identity = %q, want fallback-id (worker doesn't actually reference anything)", workerIdentity)
	}
}

func TestIdentityForService_SecretsOrConfigAlwaysUsesManagedIdentity(t *testing.T) {
	t.Parallel()
	// Compose secrets:/config: is independent of env-var usage-driven
	// discovery -- declaring it IS the usage, unlike depends_on:.
	service := &models.Service{Name: "web", Secrets: []string{"db-password"}}
	got := identityForService(service, "fallback-id", "managed-id", nil)
	if got != "managed-id" {
		t.Errorf("got %q, want managed-id (secrets: should always use the managed identity)", got)
	}
}
