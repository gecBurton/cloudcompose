package azure

import (
	"encoding/json"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"os"
	"path/filepath"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// mockAzureProdEnv matches the expected fixture exactly, including the
// fully-formed resource IDs (the azurerm provider parses these during
// `terraform validate`, so abbreviated stand-ins fail before ever reaching
// the schema checks that test exists to make) and the explicit
// container_registry_name -- without it, ContainerRegistryName's own
// hash-based naming would be used instead, which is a different (and
// also correct) value, but not the one the golden files were generated
// against.
//
// InfrastructureSubnetID/PostgresqlSubnetID/MysqlSubnetID/RedisSubnetID
// are deliberately NOT set here: InferAzure computes them itself now
// (appSubnetsAzure, from AppsCIDR + SubnetIndex), matching what a real
// `cloudcompose main` run does -- see
// docs/azure-app-isolation-design.md. SubnetIndex=0 here matches every
// golden fixture, which all assume a single app per environment.
func mockAzureProdEnv() models.AzureEnvironment {
	env := models.NewAzureEnvironment()
	env.Name = "prod"
	env.ResourceGroupName = "prod"
	env.LogAnalyticsWorkspaceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod/providers/Microsoft.OperationalInsights/workspaces/prod-logs"
	env.VnetID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod/providers/Microsoft.Network/virtualNetworks/prod-vnet"
	env.VnetName = "prod-vnet"
	env.AppsCIDR = "10.0.128.0/17"
	env.SubnetIndex = 0
	registryName := "prodacr"
	env.ContainerRegistryName = &registryName
	return env
}

// testGetNameAzure is a minimal getName closure for unit tests that
// don't otherwise need real environment/app-scoped resource naming.
func testGetNameAzure(resourceName string) string {
	return "prod-app-" + resourceName
}

// azureGoldenExamples lists every example this phase's Azure inference
// pipeline claims to fully cover. Kept explicit rather than globbing
// examples/*/expected/azure/main.tf.json for the same reason as AWS's own
// list: adding an unrelated example later shouldn't silently start
// asserting Azure parity against it.
var azureGoldenExamples = []string{
	"hello",
	"flask",
	"flask-s3",
	"build-webapp",
	"doctor",
	"flask-redis",
	"production-stack",
	"web-api",
	"minio-s3",
	"nginx-flask-mysql",
	// Added 2026-08-08 (docs/azure-aws-parity-todo.md Priority 2):
	// previously untestable on Azure since the features these examples
	// exercise (database sizing, compose secrets:/platform config:)
	// were silent no-ops.
	//
	// "scaling" was removed from this list the same day, after the size
	// table was consolidated with shared.SizeMappings (Priority 4): its
	// web service's `size: large` maps to 4 vCPU, which exceeds Azure
	// Container Apps' Consumption tier limit and is now a real,
	// intentional cloudcompose-side rejection -- see
	// TestGetCPUCoresAzure_RejectsSizeAboveConsumptionCap for the
	// dedicated test covering this, since there's no valid Azure output
	// for this example to golden-test against.
	"platform-config",
	// "compute-tuning" was added 2026-08-08 (its container-level
	// cpu/memory overrides worked correctly at the time), removed again
	// once the exact-CPU/memory-pair validation was added the same
	// week (its worker service's `size: medium` + an explicit
	// `memory: 4096` override resolved to 1.0 vCPU + 4Gi -- not one of
	// Consumption's fixed pairs, correctly rejected), then added back
	// once more (2026-08-10) after the example's worker override was
	// changed to a matched pair valid on both clouds (2.0 vCPU/4Gi,
	// via an explicit `cpu:` override alongside `memory:` rather than
	// overriding memory alone against a `size:` default) -- see
	// docs/azure-aws-parity-todo.md's Priority 4 item for the full
	// history, and TestResolveContainerResourcesAzure_AllowsMatchedExplicitOverrides
	// for the dedicated test covering the pattern this example now
	// demonstrates.
	"compute-tuning",
}

// TestInferAzure_GoldenExamplesByteIdentical mirrors
// TestInferAWS_GoldenExamplesByteIdentical: for each golden example, the
// real parser/normalizer/inference/generator pipeline runs against the
// actual compose file and mock environment, and the result is compared as
// parsed JSON against the expected
// examples/<name>/expected/azure/main.tf.json.
func TestInferAzure_GoldenExamplesByteIdentical(t *testing.T) {
	for _, name := range azureGoldenExamples {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			composePath := filepath.Join("../../../../examples", name, "compose.yml")
			expectedPath := filepath.Join("../../../../examples", name, "expected", "azure", "main.tf.json")

			if _, err := os.Stat(composePath); err != nil {
				t.Skipf("no compose.yml for %s", name)
			}
			expectedRaw, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Skipf("no expected/azure/main.tf.json for %s: %v", name, err)
			}

			composeApp, err := shared.ParseCompose(composePath)
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}
			app, err := shared.Normalize(composeApp, name)
			if err != nil {
				t.Fatalf("Normalize failed: %v", err)
			}

			env := mockAzureProdEnv()
			resources, err := InferAzure(app, &env)
			if err != nil {
				t.Fatalf("InferAzure failed: %v", err)
			}
			actual, err := GenerateAzure(resources, &env)
			if err != nil {
				t.Fatalf("GenerateAzure failed: %v", err)
			}

			var actualParsed, expectedParsed any
			if err := json.Unmarshal([]byte(actual), &actualParsed); err != nil {
				t.Fatalf("Go output is not valid JSON: %v\n%s", err, actual)
			}
			if err := json.Unmarshal(expectedRaw, &expectedParsed); err != nil {
				t.Fatalf("golden file is not valid JSON: %v", err)
			}

			actualCanonical, _ := json.Marshal(actualParsed)
			expectedCanonical, _ := json.Marshal(expectedParsed)
			if string(actualCanonical) != string(expectedCanonical) {
				t.Errorf("output differs from golden file for %s.\n--- got ---\n%s\n--- want ---\n%s",
					name, actual, string(expectedRaw))
			}
		})
	}
}

// TestGenerateAzure_Deterministic runs the same input through the full
// pipeline 6 times and diffs the output, per this phase's own review
// discipline: every new map-shaped/ordered output gets a determinism
// check as it's written.
func TestGenerateAzure_Deterministic(t *testing.T) {
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

	var first string
	for i := 0; i < 6; i++ {
		resources, err := InferAzure(app, &env)
		if err != nil {
			t.Fatalf("InferAzure run %d failed: %v", i, err)
		}
		out, err := GenerateAzure(resources, &env)
		if err != nil {
			t.Fatalf("GenerateAzure run %d failed: %v", i, err)
		}
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
}

// TestInferContainerRegistry_BuildDictKeyOrder pins that
// the docker_image build map contains context/platform/dockerfile.
// Key order itself no longer matters (structResourceBlocks sorts keys
// alphabetically at every level, same as every other resource type), so
// this checks presence and values rather than order.
func TestInferContainerRegistry_BuildDictKeyOrder(t *testing.T) {
	t.Parallel()
	buildContext := "app"
	dockerfile := "Dockerfile"
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, BuildContext: &buildContext, Dockerfile: &dockerfile},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	inferContainerRegistry(resources, app, &env, nil)

	image, ok := resources.DockerImage["web_image"]
	if !ok {
		t.Fatalf("expected a docker_image resource")
	}
	build, ok := image.Build.(map[string]any)
	if !ok {
		t.Fatalf("expected image.Build to be map[string]any, got %T", image.Build)
	}
	if len(build) != 3 {
		t.Fatalf("expected 3 build keys, got %d: %v", len(build), build)
	}
	if build["context"] != buildContext {
		t.Errorf("context = %v, want %q", build["context"], buildContext)
	}
	if build["platform"] != "linux/amd64" {
		t.Errorf("platform = %v, want linux/amd64", build["platform"])
	}
	if build["dockerfile"] != dockerfile {
		t.Errorf("dockerfile = %v, want %q", build["dockerfile"], dockerfile)
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

// TestConnectionOrderForAzure_DatabasesThenCachesThenStorage pins that
// connections are ordered databases-then-caches-then-storage (each group
// in service declaration order) -- caught as a real requirement against
// the doctor and production-stack golden files (2026-08-06), where
// alphabetically sorting connection keys put a URL env var in the wrong
// relative position whenever a service referenced more than one
// connection.
func TestConnectionOrderForAzure_DatabasesThenCachesThenStorage(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer},
			{Name: "blobs", Capability: models.CapabilityObjectStorage},
			{Name: "db", Capability: models.CapabilityDatabase},
			{Name: "cache", Capability: models.CapabilityCache},
		},
	}
	connections := map[string]models.Connection{
		"blobs": {},
		"db":    {},
		"cache": {},
	}

	order := connectionOrderForAzure(app, connections)
	want := []string{"db", "cache", "blobs"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}
