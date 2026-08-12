package azure

import "github.com/gecburton/cloudcompose/internal/models"

// testAppEnv is the standard AzureEnvironment for unit tests that build
// resources directly (not via a full InferAzure run) -- a minimal,
// consistent set of fields, not the fully-formed resource IDs
// mockAzureProdEnv (golden_test.go) needs for byte-identical fixture
// comparison. subnetIndex is a parameter (not hardcoded to 0) since
// docs/azure-app-isolation-design.md's per-app subnet allocation is
// exactly what distinguishes otherwise-identical apps sharing one
// environment -- most tests just want testAppEnv(0).
func testAppEnv(subnetIndex int) models.AzureEnvironment {
	env := models.NewAzureEnvironment()
	env.Name = "prod"
	env.ResourceGroupName = "prod"
	env.LogAnalyticsWorkspaceID = "/subscriptions/123/workspaces/prod"
	env.VnetName = "prod-vnet"
	env.AppsCIDR = "10.0.128.0/17"
	env.SubnetIndex = subnetIndex
	return env
}

// minimalGetName builds a getName closure matching InferAzure's own
// convention (env-app-resource), for tests that call an inference
// function directly rather than through InferAzure.
func minimalGetName(env, app string) func(string) string {
	return func(resource string) string {
		return env + "-" + app + "-" + resource
	}
}

// testGetNameAzure is a minimal getName closure for unit tests that
// don't otherwise need real environment/app-scoped resource naming.
func testGetNameAzure(resourceName string) string {
	return "prod-app-" + resourceName
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func keysOfAny(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func strPtr(s string) *string { return &s }

func intPtr(n int) *int { return &n }
