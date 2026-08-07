package gcp

import (
	"encoding/json"
	"testing"
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
		"prod", "us-central1", "10.0.0.0/16",
		map[string]string{"team": "platform"}, true,
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
	out, err := GenerateGcpEnvironment("prod", "us-central1", "10.0.0.0/16", nil, true)
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
	for _, field := range []string{"target", "name", "region", "vpc_id", "subnet_id", "vpc_connector_name"} {
		if _, ok := envConfig[field]; !ok {
			t.Errorf("expected output.environment.value to include %q", field)
		}
	}
	if envConfig["target"] != "gcp" {
		t.Errorf("target = %v, want gcp", envConfig["target"])
	}
}
