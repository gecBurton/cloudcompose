package azure

import (
	"encoding/json"
	"testing"
)

// TestGenerateAzureEnvironment_ValidStructure checks Azure's generator
// produces valid JSON with the expected resource types present.
//
// No azurerm_subnet/azurerm_container_app_environment assertions here
// anymore: those moved to cloudcompose main (appSubnetsAzure,
// azure/appsubnets.go), one set per app, carved out of this
// environment's own apps_cidr output -- see
// docs/azure-app-isolation-design.md. This function now only creates
// the Cloud Compose Environment layer: resource group, Log Analytics
// workspace, VNet.
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
	for _, resourceType := range []string{
		"azurerm_resource_group", "azurerm_log_analytics_workspace", "azurerm_virtual_network",
	} {
		if _, ok := resource[resourceType]; !ok {
			t.Errorf("expected %s, got resource keys %v", resourceType, keysOfAny(resource))
		}
	}
	if _, ok := resource["azurerm_subnet"]; ok {
		t.Errorf("expected no azurerm_subnet from GenerateAzureEnvironment anymore -- subnets are per-app now (appSubnetsAzure)")
	}
	if _, ok := resource["azurerm_container_app_environment"]; ok {
		t.Errorf("expected no azurerm_container_app_environment from GenerateAzureEnvironment anymore -- it's per-app now (appSubnetsAzure)")
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
// test_creates_virtual_network, test_outputs_include_required_fields,
// test_target_is_azure. No longer covers subnet delegation or the
// Container Apps Environment: both moved to cloudcompose main -- see
// TestGenerateAzureEnvironment_ValidStructure's own updated doc comment.
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
		"azurerm_virtual_network",
	} {
		rmap, ok := resource[resourceType].(map[string]any)
		if !ok {
			t.Fatalf("expected %s, got resource keys %v", resourceType, keysOfAny(resource))
		}
		if _, ok := rmap["prod"]; !ok {
			t.Errorf("expected %s.prod, got keys %v", resourceType, keysOfAny(rmap))
		}
	}

	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	for _, field := range []string{"target", "name", "region", "vnet_id", "vnet_name", "resource_group_name", "apps_cidr"} {
		if _, ok := envConfig[field]; !ok {
			t.Errorf("expected output.environment.value to include %q", field)
		}
	}
	if envConfig["target"] != "azure" {
		t.Errorf("target = %v, want azure", envConfig["target"])
	}
}

// TestGenerateAzureEnvironment_AppsCIDRIsUpperHalfOfVnet checks the
// actual CIDR math docs/azure-app-isolation-design.md's "Decided: CIDR
// math" section commits to: apps_cidr is the upper half of the VNet
// (Cidrsubnet(vnetCIDR, 1, 1)), not derived some other way that would
// happen to produce a same-sized but differently-placed range.
func TestGenerateAzureEnvironment_AppsCIDRIsUpperHalfOfVnet(t *testing.T) {
	t.Parallel()
	out, err := GenerateAzureEnvironment("prod", "eastus", "10.0.0.0/16", nil, true, false, 7, 7)
	if err != nil {
		t.Fatalf("GenerateAzureEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	if envConfig["apps_cidr"] != "10.0.128.0/17" {
		t.Errorf("apps_cidr = %v, want 10.0.128.0/17 (the upper half of 10.0.0.0/16)", envConfig["apps_cidr"])
	}
}
