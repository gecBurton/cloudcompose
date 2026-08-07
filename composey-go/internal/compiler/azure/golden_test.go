package azure

import (
	"encoding/json"
	"github.com/gecburton/composey/internal/compiler/shared"
	"os"
	"path/filepath"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

// mockAzureProdEnv mirrors tests/conftest.py's mock_azure_prod_env fixture
// exactly, including the fully-formed resource IDs (the azurerm provider
// parses these during `terraform validate`, so abbreviated stand-ins fail
// before ever reaching the schema checks that test exists to make) and the
// explicit container_registry_name -- without it, ContainerRegistryName's
// own hash-based naming would be used instead, which is a different (and
// also correct) value, but not the one the golden files were generated
// against.
func mockAzureProdEnv() models.AzureEnvironment {
	env := models.NewAzureEnvironment()
	env.Name = "prod"
	env.ContainerAppsEnvironmentName = "prod-env"
	env.LogAnalyticsWorkspaceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod/providers/Microsoft.OperationalInsights/workspaces/prod-logs"
	env.VnetID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod/providers/Microsoft.Network/virtualNetworks/prod-vnet"
	env.InfrastructureSubnetID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod/providers/Microsoft.Network/virtualNetworks/prod-vnet/subnets/infrastructure"
	registryName := "prodacr"
	env.ContainerRegistryName = &registryName
	return env
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
}

// TestInferAzure_GoldenExamplesByteIdentical mirrors
// TestInferAWS_GoldenExamplesByteIdentical: for each golden example, the
// real parser/normalizer/inference/generator pipeline runs against the
// actual compose file and mock environment, and the result is compared as
// parsed JSON against the Python-generated
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

// TestInferContainerRegistry_BuildDictKeyOrderMatchesPython pins that
// the docker_image build map contains context/platform/dockerfile.
// Key order itself no longer matters (structResourceBlocks sorts keys
// alphabetically at every level, same as every other resource type), so
// this checks presence and values rather than order.
func TestInferContainerRegistry_BuildDictKeyOrderMatchesPython(t *testing.T) {
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

// TestContainerSpecAzure_MissingConnectionFieldsRenderAsNone mirrors
// Python's f-string behavior for a Connection whose optional fields are
// unset: str(None) == "None", not an empty string. Caught as a real
// divergence against the flask-s3/minio-s3/doctor golden files
// (2026-08-06), where a bucket or cache Connection substituted into this
// Postgres-shaped URL template produced "postgresql://:@..." in Go but
// "postgresql://None:None@..." in Python.
func TestContainerSpecAzure_MissingConnectionFieldsRenderAsNone(t *testing.T) {
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
	name := "${azurerm_storage_account.blobs_storage.name}"
	connections := map[string]models.Connection{
		"blobs": {
			Host:        "${azurerm_storage_account.blobs_storage.primary_blob_endpoint}",
			Name:        &name,
			AddressedBy: "name",
		},
	}

	container := containerSpecAzure(&app.Services[0], app, &env, connections, []string{"blobs"})
	if len(container.Env) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(container.Env))
	}
	want := "postgresql://None:None@${azurerm_storage_account.blobs_storage.primary_blob_endpoint}:None/None"
	if container.Env[0].Value != want {
		t.Errorf("got %q, want %q", container.Env[0].Value, want)
	}
}

// TestConnectionOrderForAzure_MatchesPythonDictMergeOrder pins that
// connections are ordered databases-then-caches-then-storage (each group
// in service declaration order), matching Python's `connections.update(...)`
// sequence in infer() -- caught as a real divergence against the doctor
// and production-stack golden files (2026-08-06), where alphabetically
// sorting connection keys put a URL env var in the wrong relative position
// whenever a service referenced more than one connection.
func TestConnectionOrderForAzure_MatchesPythonDictMergeOrder(t *testing.T) {
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
