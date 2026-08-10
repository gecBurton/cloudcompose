package azure

import (
	"encoding/json"
	"testing"
)

// TestGenerateAzureEnvironment_ValidStructure checks Azure's generator
// produces valid JSON with the expected resource types present.
func TestGenerateAzureEnvironment_ValidStructure(t *testing.T) {
	t.Parallel()
	out, err := GenerateAzureEnvironment(
		"prod", "eastus", "10.0.0.0/16",
		map[string]string{"Team": "platform"}, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAzureEnvironment failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)
	subnets := resource["azurerm_subnet"].(map[string]any)
	for _, key := range []string{"prod_infrastructure", "prod_postgresql", "prod_mysql"} {
		if _, ok := subnets[key]; !ok {
			t.Errorf("expected subnet %s, got keys %v", key, keysOfAny(subnets))
		}
	}
}

func keysOfAny(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestGenerateAzureEnvironment_LogRetentionDaysFlowsIntoWorkspaceAndOutput
// is the Azure counterpart to
// aws.TestGenerateAwsEnvironment_LogRetentionDaysFlowsIntoOutput --
// checks the value actually reaches both the real
// azurerm_log_analytics_workspace.retention_in_days attribute (which
// used to be hardcoded to 30 with no field backing it at all) and the
// output block LoadAzureEnvironment later reads back, not just that the
// generator runs without error.
func TestGenerateAzureEnvironment_LogRetentionDaysFlowsIntoWorkspaceAndOutput(t *testing.T) {
	t.Parallel()
	out, err := GenerateAzureEnvironment(
		"prod", "eastus", "10.0.0.0/16",
		nil, true, false, 7, 90,
	)
	if err != nil {
		t.Fatalf("GenerateAzureEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)
	workspace := resource["azurerm_log_analytics_workspace"].(map[string]any)["prod"].(map[string]any)
	if workspace["retention_in_days"] != float64(90) {
		t.Errorf("azurerm_log_analytics_workspace.retention_in_days = %v, want 90", workspace["retention_in_days"])
	}
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	if envConfig["log_retention_days"] != float64(90) {
		t.Errorf("output log_retention_days = %v, want 90", envConfig["log_retention_days"])
	}
}

// TestGenerateAzureEnvironment_LogRetentionDaysClampedToAzureMinimum
// checks the real, found-by-testing-the-shared-default problem: the
// shared log_retention_days default is 7 (matching AWS's own
// long-standing default), but azurerm_log_analytics_workspace requires
// a minimum of 30 -- terraform validate rejects anything lower
// ("expected retention_in_days to be in the range (30 - 730)"). Without
// this clamp, every Azure environment.yaml relying on the default (not
// explicitly overriding log_retention_days) would generate Terraform
// that fails to validate.
func TestGenerateAzureEnvironment_LogRetentionDaysClampedToAzureMinimum(t *testing.T) {
	t.Parallel()
	out, err := GenerateAzureEnvironment(
		"prod", "eastus", "10.0.0.0/16",
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAzureEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)
	workspace := resource["azurerm_log_analytics_workspace"].(map[string]any)["prod"].(map[string]any)
	if workspace["retention_in_days"] != float64(30) {
		t.Errorf("azurerm_log_analytics_workspace.retention_in_days = %v, want 30 (clamped from the shared default of 7)", workspace["retention_in_days"])
	}
	// The output block should report the clamped value actually
	// deployed, not the raw request that was passed in -- a mismatch
	// here would make LoadAzureEnvironment lie about what's real.
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	if envConfig["log_retention_days"] != float64(30) {
		t.Errorf("output log_retention_days = %v, want 30 (clamped, matching the real resource)", envConfig["log_retention_days"])
	}
}

// TestGenerateAzureEnvironment_ComprehensiveResourcePresence mirrors
// test_creates_resource_group, test_creates_log_analytics_workspace,
// test_creates_virtual_network, test_subnet_is_delegated_to_container_apps,
// test_creates_container_app_environment, test_outputs_include_required_fields,
// test_target_is_azure.
func TestGenerateAzureEnvironment_ComprehensiveResourcePresence(t *testing.T) {
	t.Parallel()
	out, err := GenerateAzureEnvironment("prod", "eastus", "10.0.0.0/16", nil, true, false, 7, 7)
	if err != nil {
		t.Fatalf("GenerateAzureEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)

	for _, resourceType := range []string{
		"azurerm_resource_group", "azurerm_log_analytics_workspace",
		"azurerm_virtual_network", "azurerm_container_app_environment",
	} {
		rmap, ok := resource[resourceType].(map[string]any)
		if !ok {
			t.Fatalf("expected %s, got resource keys %v", resourceType, keysOfAny(resource))
		}
		if _, ok := rmap["prod"]; !ok {
			t.Errorf("expected %s.prod, got keys %v", resourceType, keysOfAny(rmap))
		}
	}

	subnets := resource["azurerm_subnet"].(map[string]any)
	infra := subnets["prod_infrastructure"].(map[string]any)
	delegation := infra["delegation"].([]any)[0].(map[string]any)
	serviceDelegation := delegation["service_delegation"].([]any)[0].(map[string]any)
	if serviceDelegation["name"] != "Microsoft.App/environments" {
		t.Errorf("service_delegation.name = %v, want Microsoft.App/environments", serviceDelegation["name"])
	}

	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	for _, field := range []string{"target", "name", "region", "container_apps_environment_name"} {
		if _, ok := envConfig[field]; !ok {
			t.Errorf("expected output.environment.value to include %q", field)
		}
	}
	if envConfig["target"] != "azure" {
		t.Errorf("target = %v, want azure", envConfig["target"])
	}
}
