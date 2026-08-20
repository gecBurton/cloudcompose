package azure

import (
	"encoding/json"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// --- output.fqdn presence: only ever exercised with ingress present.

func TestAzureIngressFQDN_NoIngressReturnsNil(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	resources.ContainerApp["web"] = models.NewContainerApp() // Ingress left nil

	if fqdn := azureIngressFQDN(resources); fqdn != nil {
		t.Errorf("expected nil, got %v", *fqdn)
	}
}

func TestGenerateAzure_NoIngressProducesNoOutputKey(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	app := models.NewContainerApp()
	app.Name = "prod-app-web"
	resources.ContainerApp["web"] = app
	env := testAppEnv(0)

	out, err := GenerateAzure(resources, &env, "app")
	if err != nil {
		t.Fatalf("GenerateAzure failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if _, ok := parsed["output"]; ok {
		t.Errorf("expected no 'output' key when no service has ingress, got %v", parsed["output"])
	}
}

// --- output.cdn_fqdn presence: docs/azure-todo.md's Front Door item --
// a clean apply had only ever been verified by polling the Container
// App's own FQDN (output.fqdn), never Front Door's actual endpoint
// hostname, so this output publishes the latter for a smoke test to
// poll directly.

func TestAzureCdnFQDN_NoCdnEndpointsReturnsNil(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()

	if fqdn := azureCdnFQDN(resources); fqdn != nil {
		t.Errorf("expected nil, got %v", *fqdn)
	}
}

func TestAzureCdnFQDN_ReturnsHostNameReference(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	resources.CdnFrontdoorEndpoint["web"] = models.FrontDoorEndpoint{Name: "prod-app-web-fd"}

	fqdn := azureCdnFQDN(resources)
	if fqdn == nil {
		t.Fatalf("expected a non-nil reference")
	}
	want := "${azurerm_cdn_frontdoor_endpoint.web.host_name}"
	if *fqdn != want {
		t.Errorf("got %q, want %q", *fqdn, want)
	}
}

func TestAzureCdnFQDN_MultipleEndpointsPicksDeterministicFirst(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	resources.CdnFrontdoorEndpoint["web"] = models.FrontDoorEndpoint{Name: "web"}
	resources.CdnFrontdoorEndpoint["api"] = models.FrontDoorEndpoint{Name: "api"}

	fqdn := azureCdnFQDN(resources)
	if fqdn == nil {
		t.Fatalf("expected a non-nil reference")
	}
	// Sorted alphabetically, same convention as azureIngressFQDN's own
	// "first" match -- "api" sorts before "web".
	want := "${azurerm_cdn_frontdoor_endpoint.api.host_name}"
	if *fqdn != want {
		t.Errorf("got %q, want %q (deterministic, sorted first)", *fqdn, want)
	}
}

func TestGenerateAzure_CdnFqdnOutputCoexistsWithIngressFqdn(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	app := models.NewContainerApp()
	app.Name = "prod-app-web"
	app.Ingress = &models.ContainerAppIngress{}
	resources.ContainerApp["web"] = app
	resources.CdnFrontdoorEndpoint["web"] = models.FrontDoorEndpoint{Name: "prod-app-web-fd"}
	env := testAppEnv(0)

	out, err := GenerateAzure(resources, &env, "app")
	if err != nil {
		t.Fatalf("GenerateAzure failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	output, ok := parsed["output"].(map[string]any)
	if !ok {
		t.Fatalf("expected an 'output' key, got %v", parsed["output"])
	}
	if _, ok := output["fqdn"]; !ok {
		t.Errorf("expected output.fqdn to still be present alongside output.cdn_fqdn")
	}
	if _, ok := output["cdn_fqdn"]; !ok {
		t.Errorf("expected output.cdn_fqdn to be present")
	}
}

// TestGenerateAzure_Deterministic runs the same input through the full
// pipeline 6 times and diffs the output, per this phase's own review
// discipline: every new map-shaped/ordered output gets a determinism
// check as it's written.
func TestGenerateAzure_Deterministic(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/doctor/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "doctor")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := mockAzureProdEnv()

	var first string
	for i := 0; i < 6; i++ {
		resources, err := InferAzure(app, &env)
		if err != nil {
			t.Fatalf("InferAzure run %d failed: %v", i, err)
		}
		out, err := GenerateAzure(resources, &env, "app")
		if err != nil {
			t.Fatalf("GenerateAzure run %d failed: %v", i, err)
		}
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
}

// TestInferContainerRegistry_BuildDictKeyOrder pins that
// the docker_image build map contains context/platform/dockerfile.
// Key order itself no longer matters (structResourceBlocks sorts keys
// alphabetically at every level, same as every other resource type), so
// this checks presence and values rather than order.
func TestInferContainerRegistry_BuildDictKeyOrder(t *testing.T) {
	t.Parallel()
	buildContext := "app"
	dockerfile := "Dockerfile"
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, BuildContext: &buildContext, Dockerfile: &dockerfile},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	inferContainerRegistry(resources, app, &env, nil)

	image, ok := resources.DockerImage["web_image"]
	if !ok {
		t.Fatalf("expected a docker_image resource")
	}
	build, ok := image.Build.(map[string]any)
	if !ok {
		t.Fatalf("expected image.Build to be map[string]any, got %T", image.Build)
	}
	if len(build) != 3 {
		t.Fatalf("expected 3 build keys, got %d: %v", len(build), build)
	}
	if build["context"] != buildContext {
		t.Errorf("context = %v, want %q", build["context"], buildContext)
	}
	if build["platform"] != "linux/amd64" {
		t.Errorf("platform = %v, want linux/amd64", build["platform"])
	}
	if build["dockerfile"] != dockerfile {
		t.Errorf("dockerfile = %v, want %q", build["dockerfile"], dockerfile)
	}
}

// TestGenerateAzure_NilEnvBackendOmitsBackendBlock mirrors
// aws.TestGenerateAWS_NilEnvBackendOmitsBackendBlock for the Azure
// app-level generator. See docs/multi-user-state.md.
func TestGenerateAzure_NilEnvBackendOmitsBackendBlock(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	env := testAppEnv(0)

	out, err := GenerateAzure(resources, &env, "checkout-api")
	if err != nil {
		t.Fatalf("GenerateAzure failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if _, ok := parsed["terraform"].(map[string]any)["backend"]; ok {
		t.Errorf("did not expect terraform.backend when env.Backend is nil")
	}
}

// TestGenerateAzure_EnvBackendProducesAppSpecificBackendBlock mirrors
// aws.TestGenerateAWS_EnvBackendProducesAppSpecificBackendBlock for the
// Azure app-level generator.
func TestGenerateAzure_EnvBackendProducesAppSpecificBackendBlock(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	env := testAppEnv(0)
	env.Backend = &models.BackendConfig{
		Azure: &models.AzureBackendConfig{
			ResourceGroupName:  "my-org-tfstate-rg",
			StorageAccountName: "myorgtfstate",
			ContainerName:      "tfstate",
		},
	}

	out, err := GenerateAzure(resources, &env, "checkout-api")
	if err != nil {
		t.Fatalf("GenerateAzure failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	azurermBackend, ok := parsed["terraform"].(map[string]any)["backend"].(map[string]any)["azurerm"].(map[string]any)
	if !ok {
		t.Fatalf("expected terraform.backend.azurerm, got %v", parsed["terraform"])
	}
	wantKey := "cloudcompose/prod/apps/checkout-api.tfstate"
	if azurermBackend["key"] != wantKey {
		t.Errorf("key = %v, want %v", azurermBackend["key"], wantKey)
	}
	if azurermBackend["storage_account_name"] != "myorgtfstate" {
		t.Errorf("storage_account_name = %v, want myorgtfstate", azurermBackend["storage_account_name"])
	}
}
