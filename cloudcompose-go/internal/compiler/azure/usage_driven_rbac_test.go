package azure

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

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
