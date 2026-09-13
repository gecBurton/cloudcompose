package gcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
)

// gcpGoldenExamples lists every example this phase's GCP inference
// pipeline claims to fully cover -- the same set AWS/Azure's own golden
// lists cover, since the whole point of the shared example set is that
// all three clouds are exercised against the same compose files unless
// a cloud genuinely can't support one (see AGENTS.md's "AWS-first but
// cloud-agnostic" note: GCP's own coverage here is intentionally lighter
// in verification depth, never deployed for real, but the example set
// itself is not narrowed further than AWS/Azure's).
var gcpGoldenExamples = []string{
	"hello",
	"doctor",
	"scaling",
	"compute-tuning",
	"platform-config",
	"production-stack",
	"web-api",
}

// TestInferGcp_GoldenExamplesByteIdentical mirrors
// TestInferAWS_GoldenExamplesByteIdentical/TestInferAzure_GoldenExamplesByteIdentical:
// for each golden example, the real parser/normalizer/inference/generator
// pipeline runs against the actual compose file and mock environment, and
// the result is compared as parsed JSON against the expected
// examples/<name>/expected/gcp/main.tf.json. Unlike AWS/Azure, these
// fixtures have never been checked against a real deployment (see
// AGENTS.md) -- this test only pins today's output as a regression
// baseline, not a correctness claim.
func TestInferGcp_GoldenExamplesByteIdentical(t *testing.T) {
	for _, name := range gcpGoldenExamples {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			composePath := filepath.Join("../../../../examples", name, "compose.yml")
			expectedPath := filepath.Join("../../../../examples", name, "expected", "gcp", "main.tf.json")

			if _, err := os.Stat(composePath); err != nil {
				t.Skipf("no compose.yml for %s", name)
			}
			expectedRaw, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Skipf("no expected/gcp/main.tf.json for %s: %v", name, err)
			}

			composeApp, err := shared.ParseCompose(composePath)
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}
			app, err := shared.Normalize(composeApp, name)
			if err != nil {
				t.Fatalf("Normalize failed: %v", err)
			}

			env := gcpTestEnv()
			resources := InferGcp(app, &env)
			actual, err := GenerateGcp(resources, &env, "app")
			if err != nil {
				t.Fatalf("GenerateGcp failed: %v", err)
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
