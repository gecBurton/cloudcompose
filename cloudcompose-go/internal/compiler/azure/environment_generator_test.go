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
		map[string]string{"Team": "platform"}, true, false, 7,
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

// TestGenerateAzureEnvironment_ComprehensiveResourcePresence mirrors
// test_creates_resource_group, test_creates_log_analytics_workspace,
// test_creates_virtual_network, test_subnet_is_delegated_to_container_apps,
// test_creates_container_app_environment, test_outputs_include_required_fields,
// test_target_is_azure.
func TestGenerateAzureEnvironment_ComprehensiveResourcePresence(t *testing.T) {
	t.Parallel()
	out, err := GenerateAzureEnvironment("prod", "eastus", "10.0.0.0/16", nil, true, false, 7)
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
