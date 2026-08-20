package gcp

import (
	"encoding/json"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

func keysOfAny(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestGenerateGcpEnvironment_ValidStructure checks GCP's generator
// produces valid JSON with the expected resource types present.
func TestGenerateGcpEnvironment_ValidStructure(t *testing.T) {
	t.Parallel()
	out, err := GenerateGcpEnvironment(
		"prod", "us-central1", "10.0.0.0/16", "my-gcp-project", "",
		map[string]string{"team": "platform"}, true, nil,
	)
	if err != nil {
		t.Fatalf("GenerateGcpEnvironment failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)
	if _, ok := resource["google_compute_network"]; !ok {
		t.Errorf("expected google_compute_network")
	}
	if _, ok := resource["google_vpc_access_connector"]; !ok {
		t.Errorf("expected google_vpc_access_connector")
	}
}

// TestGenerateGcpEnvironment_ComprehensiveResourcePresence mirrors
// test_creates_subnetwork, test_creates_service_networking,
// test_outputs_include_required_fields, test_target_is_gcp.
func TestGenerateGcpEnvironment_ComprehensiveResourcePresence(t *testing.T) {
	t.Parallel()
	out, err := GenerateGcpEnvironment("prod", "us-central1", "10.0.0.0/16", "my-gcp-project", "", nil, true, nil)
	if err != nil {
		t.Fatalf("GenerateGcpEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)

	for _, resourceType := range []string{
		"google_compute_subnetwork", "google_compute_global_address",
		"google_service_networking_connection",
	} {
		if _, ok := resource[resourceType]; !ok {
			t.Errorf("expected %s, got resource keys %v", resourceType, keysOfAny(resource))
		}
	}

	provider := parsed["provider"].(map[string]any)["google"].(map[string]any)
	if provider["region"] != "us-central1" {
		t.Errorf("provider.google.region = %v, want us-central1", provider["region"])
	}

	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	for _, field := range []string{"target", "name", "region", "project_id", "vpc_id", "subnet_id", "vpc_connector_name"} {
		if _, ok := envConfig[field]; !ok {
			t.Errorf("expected output.environment.value to include %q", field)
		}
	}
	if envConfig["target"] != "gcp" {
		t.Errorf("target = %v, want gcp", envConfig["target"])
	}
	if envConfig["project_id"] != "my-gcp-project" {
		t.Errorf("project_id = %v, want my-gcp-project", envConfig["project_id"])
	}
}

// TestGenerateGcpEnvironment_DomainFlowsThroughWhenSet mirrors the
// project_id test above: domain is a required decision for GCP CDN
// (see docs/spikes/gcp/README.md and models.GcpEnvironment.Domain's own
// doc comment), so it should appear in the output when passed and be
// omitted when not.
func TestGenerateGcpEnvironment_DomainFlowsThroughWhenSet(t *testing.T) {
	t.Parallel()
	out, err := GenerateGcpEnvironment("prod", "us-central1", "10.0.0.0/16", "my-gcp-project", "example.com", nil, true, nil)
	if err != nil {
		t.Fatalf("GenerateGcpEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	if envConfig["domain"] != "example.com" {
		t.Errorf("domain = %v, want example.com", envConfig["domain"])
	}
}

func TestGenerateGcpEnvironment_DomainOmittedWhenNotSet(t *testing.T) {
	t.Parallel()
	out, err := GenerateGcpEnvironment("prod", "us-central1", "10.0.0.0/16", "my-gcp-project", "", nil, true, nil)
	if err != nil {
		t.Fatalf("GenerateGcpEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	if _, ok := envConfig["domain"]; ok {
		t.Errorf("did not expect domain when not passed, got %v", envConfig["domain"])
	}
}

// --- Backend coverage (docs/multi-user-state.md) --------------------------

// TestGenerateGcpEnvironment_NilBackendOmitsBackendBlock mirrors
// aws.TestGenerateAwsEnvironment_NilBackendOmitsBackendBlock: today's
// default (no backend: configured) emits no terraform.backend block and
// no output.backend block.
func TestGenerateGcpEnvironment_NilBackendOmitsBackendBlock(t *testing.T) {
	t.Parallel()
	out, err := GenerateGcpEnvironment("prod", "us-central1", "10.0.0.0/16", "my-gcp-project", "", nil, true, nil)
	if err != nil {
		t.Fatalf("GenerateGcpEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	terraform := parsed["terraform"].(map[string]any)
	if _, ok := terraform["backend"]; ok {
		t.Errorf("did not expect terraform.backend when backend config is nil")
	}
	output := parsed["output"].(map[string]any)
	if _, ok := output["backend"]; ok {
		t.Errorf("did not expect output.backend when backend config is nil")
	}
}

// TestGenerateGcpEnvironment_BackendEmitsGcsBlockWithDerivedPrefix
// confirms a configured backend.gcp produces a
// `terraform { backend "gcs" {} }` block whose "prefix" (gcs's own name
// for the per-object path within the bucket, unlike s3/azurerm's "key")
// is mechanically derived from the environment's own name, never
// authored -- mirroring
// aws.TestGenerateAwsEnvironment_BackendEmitsS3BlockWithDerivedKey.
func TestGenerateGcpEnvironment_BackendEmitsGcsBlockWithDerivedPrefix(t *testing.T) {
	t.Parallel()
	backend := &models.BackendConfig{
		Gcp: &models.GcpBackendConfig{Bucket: "my-org-tfstate"},
	}
	out, err := GenerateGcpEnvironment("prod", "us-central1", "10.0.0.0/16", "my-gcp-project", "", nil, true, backend)
	if err != nil {
		t.Fatalf("GenerateGcpEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	gcsBackend, ok := parsed["terraform"].(map[string]any)["backend"].(map[string]any)["gcs"].(map[string]any)
	if !ok {
		t.Fatalf("expected terraform.backend.gcs, got %v", parsed["terraform"])
	}
	if gcsBackend["bucket"] != "my-org-tfstate" {
		t.Errorf("bucket = %v, want my-org-tfstate", gcsBackend["bucket"])
	}
	if gcsBackend["prefix"] != "cloudcompose/prod/environment.tfstate" {
		t.Errorf("prefix = %v, want cloudcompose/prod/environment.tfstate", gcsBackend["prefix"])
	}
}

// TestGenerateGcpEnvironment_BackendOutputSurfacesFactsForApps mirrors
// aws.TestGenerateAwsEnvironment_BackendOutputSurfacesFactsForApps: the
// output.backend block carries the same bucket fact LoadGcpEnvironment
// will hand back to `cloudcompose compile` for every app compiled
// against this environment, but deliberately not the prefix -- apps
// must derive their own via shared.BackendKeyForApp, not reuse this
// environment's.
func TestGenerateGcpEnvironment_BackendOutputSurfacesFactsForApps(t *testing.T) {
	t.Parallel()
	backend := &models.BackendConfig{
		Gcp: &models.GcpBackendConfig{Bucket: "my-org-tfstate"},
	}
	out, err := GenerateGcpEnvironment("prod", "us-central1", "10.0.0.0/16", "my-gcp-project", "", nil, true, backend)
	if err != nil {
		t.Fatalf("GenerateGcpEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	backendOutput, ok := parsed["output"].(map[string]any)["backend"].(map[string]any)["value"].(map[string]any)
	if !ok {
		t.Fatalf("expected output.backend.value, got %v", parsed["output"])
	}
	if backendOutput["provider"] != "gcp" {
		t.Errorf("provider = %v, want gcp", backendOutput["provider"])
	}
	gcpBackendOutput, ok := backendOutput["gcp"].(map[string]any)
	if !ok {
		t.Fatalf("expected output.backend.value.gcp, got %v", backendOutput)
	}
	if gcpBackendOutput["bucket"] != "my-org-tfstate" {
		t.Errorf("bucket = %v, want my-org-tfstate", gcpBackendOutput["bucket"])
	}
	if _, ok := gcpBackendOutput["prefix"]; ok {
		t.Errorf("did not expect output.backend.value.gcp.prefix -- apps must derive their own, not reuse this environment's")
	}
}
