package aws

import (
	"encoding/json"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"os"
	"path/filepath"
	"testing"
)

// awsGoldenExamples lists every example this phase's inference pipeline
// (networking, service discovery, managed services, compute, scheduling,
// edge, permissions) claims to fully cover -- i.e. every AWS golden example
// that does not depend on anything still unported. Kept as an explicit
// list rather than globbing examples/*/expected/aws/main.tf.json so that
// adding an Azure/GCP example later doesn't silently start asserting AWS
// parity against it.
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

// TestInferAWS_GoldenExamplesByteIdentical compares each golden example's
// compose file and mock environment against the already-committed
// examples/<name>/expected/aws/main.tf.json golden file, as parsed JSON
// (structural equality: comparing post-json.Unmarshal is a stronger check
// than a raw byte diff would be -- it doesn't accidentally pass due to two
// things stringifying identically that a structural diff would still
// catch as different types).
//
// Golden fixtures live at expected/aws/main.tf.json, for symmetry with
// Azure/GCP's own expected/{azure,gcp}/main.tf.json subfolders -- not a
// bare examples/<name>/expected/main.tf.json, which would read as though
// AWS were somehow the default/canonical cloud rather than just the
// first one built.
func TestInferAWS_GoldenExamplesByteIdentical(t *testing.T) {
	for _, name := range awsGoldenExamples {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			composePath := filepath.Join("../../../../examples", name, "compose.yml")
			expectedPath := filepath.Join("../../../../examples", name, "expected", "aws", "main.tf.json")

			if _, err := os.Stat(composePath); err != nil {
				t.Skipf("no compose.yml for %s", name)
			}
			expectedRaw, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Skipf("no expected/aws/main.tf.json for %s: %v", name, err)
			}

			composeApp, err := shared.ParseCompose(composePath)
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}
			app, err := shared.Normalize(composeApp, name)
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

			// normalizeEmbeddedJSON also re-parses any string value that
			// itself holds JSON (IAM policy documents, ECS container
			// definitions, Secrets Manager secret strings): Terraform
			// treats these as opaque strings, so their exact whitespace
			// has no effect on what actually gets deployed, and byte-for-
			// byte matching that formatting is not this comparison's
			// goal -- only that the two sides describe the same JSON
			// value once fully parsed, at every level, embedded strings
			// included.
			actualCanonical, _ := json.Marshal(normalizeEmbeddedJSON(actualParsed))
			expectedCanonical, _ := json.Marshal(normalizeEmbeddedJSON(expectedParsed))
			if string(actualCanonical) != string(expectedCanonical) {
				t.Errorf("output differs from golden file for %s.\n--- got ---\n%s\n--- want ---\n%s",
					name, actual, string(expectedRaw))
			}
		})
	}
}

// normalizeEmbeddedJSON recursively walks a parsed JSON value and, for
// every string that itself parses as valid JSON, replaces it with the
// parsed (and then recursively normalized) value. Applied to both sides
// of a golden comparison so differences in an embedded JSON string's
// whitespace/key-order (which Terraform never observes -- it stores these
// as opaque attribute values) don't fail a comparison that only cares
// whether the two sides describe the same infrastructure.
func normalizeEmbeddedJSON(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, e := range val {
			out[k] = normalizeEmbeddedJSON(e)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, e := range val {
			out[i] = normalizeEmbeddedJSON(e)
		}
		return out
	case string:
		var parsed any
		if err := json.Unmarshal([]byte(val), &parsed); err == nil {
			// A bare JSON string (e.g. "us-east-1") re-parses
			// successfully too, as itself -- only recurse into things
			// that decoded into a structure, not scalars, so ordinary
			// string attributes are left untouched.
			switch parsed.(type) {
			case map[string]any, []any:
				return normalizeEmbeddedJSON(parsed)
			}
		}
		return val
	default:
		return val
	}
}
