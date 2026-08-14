package azure

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// fakeContainerAppsClient is a minimal in-memory stand-in for
// *armappcontainers.ContainerAppsClient, keyed by Container App name,
// mirroring aws/status_test.go's fakeECSClient rationale.
type fakeContainerAppsClient struct {
	apps map[string]armappcontainers.ContainerApp
	// seenNames records every containerAppName Get was called with, so
	// tests can assert FetchStatus queried exactly the Azure-side names
	// InferAzure's own getName closure would have created.
	seenNames []string
}

func (f *fakeContainerAppsClient) Get(ctx context.Context, resourceGroupName, containerAppName string, options *armappcontainers.ContainerAppsClientGetOptions) (armappcontainers.ContainerAppsClientGetResponse, error) {
	f.seenNames = append(f.seenNames, containerAppName)
	app, ok := f.apps[containerAppName]
	if !ok {
		return armappcontainers.ContainerAppsClientGetResponse{}, notFoundError()
	}
	return armappcontainers.ContainerAppsClientGetResponse{ContainerApp: app}, nil
}

// fakeRevisionsClient is a minimal in-memory stand-in for
// *armappcontainers.ContainerAppsRevisionsClient, keyed by revision name.
type fakeRevisionsClient struct {
	revisions map[string]armappcontainers.Revision
}

func (f *fakeRevisionsClient) GetRevision(ctx context.Context, resourceGroupName, containerAppName, revisionName string, options *armappcontainers.ContainerAppsRevisionsClientGetRevisionOptions) (armappcontainers.ContainerAppsRevisionsClientGetRevisionResponse, error) {
	rev, ok := f.revisions[revisionName]
	if !ok {
		return armappcontainers.ContainerAppsRevisionsClientGetRevisionResponse{}, notFoundError()
	}
	return armappcontainers.ContainerAppsRevisionsClientGetRevisionResponse{Revision: rev}, nil
}

func notFoundError() error {
	return &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "NotFound"}
}

func i32Ptr(i int32) *int32 { return &i }

// TestFetchStatus_RealHelloExample exercises the real hello example
// (one public "web" service) through the real parser/normalizer
// boundary, per this codebase's own real-boundary testing discipline
// (AGENTS.md).
func TestFetchStatus_RealHelloExample(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "hello")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := mockAzureProdEnv()

	// "prod-hello-web" is exactly what InferAzure's own getName closure
	// would compute for this env/app/service combination.
	succeeded := armappcontainers.ContainerAppProvisioningStateSucceeded
	healthy := armappcontainers.RevisionHealthStateHealthy
	appsC := &fakeContainerAppsClient{
		apps: map[string]armappcontainers.ContainerApp{
			"prod-hello-web": {
				Properties: &armappcontainers.ContainerAppProperties{
					ProvisioningState:  &succeeded,
					LatestRevisionName: strPtr("prod-hello-web--abc123"),
				},
			},
		},
	}
	revC := &fakeRevisionsClient{
		revisions: map[string]armappcontainers.Revision{
			"prod-hello-web--abc123": {
				Properties: &armappcontainers.RevisionProperties{
					Replicas:    i32Ptr(2),
					HealthState: &healthy,
				},
			},
		},
	}

	statuses, err := FetchStatus(context.Background(), appsC, revC, app, &env)
	if err != nil {
		t.Fatalf("FetchStatus failed: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected exactly 1 status, got %d: %+v", len(statuses), statuses)
	}

	got := statuses[0]
	if got.Name != "web" {
		t.Errorf("Name = %q, want web", got.Name)
	}
	if got.AzureName != "prod-hello-web" {
		t.Errorf("AzureName = %q, want prod-hello-web", got.AzureName)
	}
	if !got.Found {
		t.Fatal("expected Found = true")
	}
	if got.ProvisioningState != "Succeeded" {
		t.Errorf("ProvisioningState = %q, want Succeeded", got.ProvisioningState)
	}
	if got.Replicas != 2 {
		t.Errorf("Replicas = %d, want 2", got.Replicas)
	}
	if got.HealthState != "Healthy" {
		t.Errorf("HealthState = %q, want Healthy", got.HealthState)
	}
	if !got.HasIngress {
		t.Error("expected HasIngress = true (hello's web service declares a port)")
	}

	if len(appsC.seenNames) != 1 || appsC.seenNames[0] != "prod-hello-web" {
		t.Errorf("Get called with names %v, want exactly [prod-hello-web]", appsC.seenNames)
	}
}

// TestFetchStatus_NotYetDeployed confirms a service Azure has never
// heard of (not yet deployed, or deployed under a name that no longer
// matches) is reported as not-found rather than causing an error.
func TestFetchStatus_NotYetDeployed(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "hello")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := mockAzureProdEnv()

	appsC := &fakeContainerAppsClient{apps: map[string]armappcontainers.ContainerApp{}}
	revC := &fakeRevisionsClient{}

	statuses, err := FetchStatus(context.Background(), appsC, revC, app, &env)
	if err != nil {
		t.Fatalf("FetchStatus failed: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected exactly 1 status, got %d", len(statuses))
	}
	if statuses[0].Found {
		t.Error("expected Found = false for a service Azure has no record of")
	}
}

// TestFetchStatus_SkipsScheduledAndNonContainerServices confirms ps
// only ever asks Azure about services InferAzure itself would actually
// create a Container App for -- scheduled services become Container
// App Jobs instead (compute.go's inferScheduledJobs/inferContainerApps
// split), and non-container capabilities never get either.
func TestFetchStatus_SkipsScheduledAndNonContainerServices(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/nginx-flask-mysql/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "nginx-flask-mysql")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := mockAzureProdEnv()

	appsC := &fakeContainerAppsClient{apps: map[string]armappcontainers.ContainerApp{}}
	revC := &fakeRevisionsClient{}

	statuses, err := FetchStatus(context.Background(), appsC, revC, app, &env)
	if err != nil {
		t.Fatalf("FetchStatus failed: %v", err)
	}

	for _, s := range statuses {
		for i := range app.Services {
			if app.Services[i].Name == s.Name && app.Services[i].Capability != models.CapabilityContainer {
				t.Errorf("expected no status row for non-container service %q", s.Name)
			}
		}
	}
}

func TestSubscriptionIDFromResourceID(t *testing.T) {
	tests := []struct {
		resourceID string
		want       string
		wantErr    bool
	}{
		{"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod/providers/Microsoft.OperationalInsights/workspaces/prod-logs", "00000000-0000-0000-0000-000000000000", false},
		{"/subscriptions/123/workspaces/prod", "123", false},
		{"not-a-resource-id", "", true},
	}
	for _, tc := range tests {
		got, err := SubscriptionIDFromResourceID(tc.resourceID)
		if tc.wantErr {
			if err == nil {
				t.Errorf("SubscriptionIDFromResourceID(%q): expected an error, got %q", tc.resourceID, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("SubscriptionIDFromResourceID(%q) failed: %v", tc.resourceID, err)
		}
		if got != tc.want {
			t.Errorf("SubscriptionIDFromResourceID(%q) = %q, want %q", tc.resourceID, got, tc.want)
		}
	}
}
