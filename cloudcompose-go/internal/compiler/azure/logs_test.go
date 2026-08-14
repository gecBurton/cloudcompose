package azure

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// fakeLogsClient is a minimal in-memory stand-in for
// *azquery.LogsClient, keyed by resource ID, mirroring
// status_test.go's fakeContainerAppsClient rationale.
type fakeLogsClient struct {
	tables map[string][]*azquery.Table
	// seenResourceIDs records every resourceID QueryResource was called
	// with, so tests can assert FetchLogs queried exactly the Container
	// App resource IDs status.go's own naming would have created.
	seenResourceIDs []string
}

func (f *fakeLogsClient) QueryResource(ctx context.Context, resourceID string, body azquery.Body, options *azquery.LogsClientQueryResourceOptions) (azquery.LogsClientQueryResourceResponse, error) {
	f.seenResourceIDs = append(f.seenResourceIDs, resourceID)
	tables, ok := f.tables[resourceID]
	if !ok {
		return azquery.LogsClientQueryResourceResponse{}, notFoundError()
	}
	return azquery.LogsClientQueryResourceResponse{Results: azquery.Results{Tables: tables}}, nil
}

// consoleLogsTable builds a fake ContainerAppConsoleLogs_CL result
// table with the TimeGenerated/Log_s columns FetchLogs looks for.
func consoleLogsTable(rows ...azquery.Row) *azquery.Table {
	name := "PrimaryResult"
	timeCol, msgCol := "TimeGenerated", "Log_s"
	return &azquery.Table{
		Name: &name,
		Columns: []*azquery.Column{
			{Name: &timeCol},
			{Name: &msgCol},
		},
		Rows: rows,
	}
}

// TestFetchLogs_RealHelloExample exercises the real hello example (one
// "web" service) through the real parser/normalizer boundary, per this
// codebase's own real-boundary testing discipline (AGENTS.md).
func TestFetchLogs_RealHelloExample(t *testing.T) {
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

	// This is exactly the resource ID status.go's own azureName
	// ("prod-hello-web") would resolve to, wrapped in the standard
	// Container App ARM ID shape.
	resourceID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod/providers/Microsoft.App/containerApps/prod-hello-web"
	t1 := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	t2 := time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)

	client := &fakeLogsClient{
		tables: map[string][]*azquery.Table{
			resourceID: {
				consoleLogsTable(
					azquery.Row{t2, "second"},
					azquery.Row{t1, "first"},
				),
			},
		},
	}

	events, err := FetchLogs(context.Background(), client, "00000000-0000-0000-0000-000000000000", app, &env, nil, time.Time{}, 200)
	if err != nil {
		t.Fatalf("FetchLogs failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}
	// Sorted chronologically regardless of the order the fake returned
	// them in.
	if events[0].Message != "first" || events[1].Message != "second" {
		t.Errorf("events not sorted by timestamp: %+v", events)
	}
	if events[0].Service != "web" {
		t.Errorf("Service = %q, want web", events[0].Service)
	}

	if len(client.seenResourceIDs) != 1 || client.seenResourceIDs[0] != resourceID {
		t.Errorf("QueryResource called with resource IDs %v, want exactly [%s]", client.seenResourceIDs, resourceID)
	}
}

// TestFetchLogs_NotYetDeployedIsNotAnError confirms a service Azure has
// no logs for (never deployed, or deployed under a since-changed name)
// is silently skipped rather than failing the whole command -- the
// same principle status.go's ServiceStatus.Found follows.
func TestFetchLogs_NotYetDeployedIsNotAnError(t *testing.T) {
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

	client := &fakeLogsClient{tables: map[string][]*azquery.Table{}}

	events, err := FetchLogs(context.Background(), client, "00000000-0000-0000-0000-000000000000", app, &env, nil, time.Time{}, 200)
	if err != nil {
		t.Fatalf("FetchLogs failed: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected no events, got %d", len(events))
	}
}

// TestFetchLogs_FiltersToNamedServices confirms passing explicit
// service names (like `cloudcompose logs web`) only queries those
// services' Container Apps, not every container service in the app.
func TestFetchLogs_FiltersToNamedServices(t *testing.T) {
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

	var containerService string
	for i := range app.Services {
		if app.Services[i].Capability == models.CapabilityContainer {
			containerService = app.Services[i].Name
			break
		}
	}
	if containerService == "" {
		t.Fatal("expected at least one container-capability service in nginx-flask-mysql")
	}

	client := &fakeLogsClient{tables: map[string][]*azquery.Table{}}

	if _, err := FetchLogs(context.Background(), client, "00000000-0000-0000-0000-000000000000", app, &env, []string{containerService}, time.Time{}, 200); err != nil {
		t.Fatalf("FetchLogs failed: %v", err)
	}

	if len(client.seenResourceIDs) != 1 {
		t.Fatalf("expected exactly 1 resource queried when filtering to one service, got %d: %v", len(client.seenResourceIDs), client.seenResourceIDs)
	}
}
