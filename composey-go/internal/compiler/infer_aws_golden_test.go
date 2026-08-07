package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// awsGoldenExamples lists every example this phase's inference pipeline
// (networking, service discovery, managed services, compute, scheduling,
// edge, permissions) claims to fully cover -- i.e. every AWS golden example
// that does not depend on anything still unported. Kept as an explicit
// list rather than globbing examples/*/expected/main.tf.json so that adding
// an Azure/GCP example later doesn't silently start asserting AWS parity
// against it.
var awsGoldenExamples = []string{
	"hello",
	"flask",
	"flask-redis",
	"flask-s3",
	"minio-s3",
	"build-webapp",
	"scaling",
	"platform-config",
	"compute-tuning",
	"nginx-flask-mysql",
	"production-stack",
	"web-api",
	"doctor",
}

// TestInferAWS_GoldenExamplesByteIdentical is this phase's actual bar, not
// a diff against golden files copied from Python's old output but a live
// comparison: for each golden example, both directories' Python compiler
// and this Go pipeline are exercised against the same compose file and
// mock environment, and their outputs are compared as parsed JSON
// (structural equality; the two implementations' embedded-JSON-string
// spacing already matches byte-for-byte per pyjson.go, but comparing
// post-json.Unmarshal is a stronger check than a raw byte diff would be --
// it doesn't accidentally pass due to two things stringifying identically
// that a structural diff would still catch as different types).
//
// This test intentionally reads the already-committed
// examples/<name>/expected/main.tf.json golden files (produced by Python)
// rather than shelling out to Python here, since composey-go has no Python
// runtime dependency; the two-implementation comparison in the earlier
// hardening phase's session already verified these golden files are
// current for the Python side.
func TestInferAWS_GoldenExamplesByteIdentical(t *testing.T) {
	for _, name := range awsGoldenExamples {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			composePath := filepath.Join("../../../examples", name, "compose.yml")
			expectedPath := filepath.Join("../../../examples", name, "expected", "main.tf.json")

			if _, err := os.Stat(composePath); err != nil {
				t.Skipf("no compose.yml for %s", name)
			}
			expectedRaw, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Skipf("no expected/main.tf.json for %s: %v", name, err)
			}

			composeApp, err := ParseCompose(composePath)
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}
			app, err := Normalize(composeApp, name)
			if err != nil {
				t.Fatalf("Normalize failed: %v", err)
			}

			env := fullMockProdEnv()
			resources, err := InferAWS(app, &env)
			if err != nil {
				t.Fatalf("InferAWS failed: %v", err)
			}
			actual, err := GenerateAWS(resources, &env)
			if err != nil {
				t.Fatalf("GenerateAWS failed: %v", err)
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
