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
