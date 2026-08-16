package azure

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// fakeLogsClient is a minimal in-memory stand-in for
// *azquery.LogsClient, keyed by resource ID, mirroring
// status_test.go's fakeContainerAppsClient rationale.
type fakeLogsClient struct {
	tables map[string][]*azquery.Table
	// errs lets a test force QueryResource to return a specific error
	// for a given resourceID -- e.g. missingLogTableError() below, to
	// exercise isMissingLogTable's own effect on FetchLogs without a
	// real Azure workspace.
	errs map[string]error
	// seenResourceIDs records every resourceID QueryResource was called
	// with, so tests can assert FetchLogs queried exactly the Container
	// App resource IDs status.go's own naming would have created.
	seenResourceIDs []string
}

func (f *fakeLogsClient) QueryResource(ctx context.Context, resourceID string, body azquery.Body, options *azquery.LogsClientQueryResourceOptions) (azquery.LogsClientQueryResourceResponse, error) {
	f.seenResourceIDs = append(f.seenResourceIDs, resourceID)
	if err, ok := f.errs[resourceID]; ok {
		return azquery.LogsClientQueryResourceResponse{}, err
	}
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

// pgLogsTable builds a fake PGSQLServerLogs result table with the
// TimeGenerated/Message columns FetchLogs looks for -- deliberately
// using "Message" rather than consoleLogsTable's own "Log_s", matching
// the real column-name difference between the two resource types this
// file queries.
func pgLogsTable(rows ...azquery.Row) *azquery.Table {
	name := "PrimaryResult"
	timeCol, msgCol := "TimeGenerated", "Message"
	return &azquery.Table{
		Name: &name,
		Columns: []*azquery.Column{
			{Name: &timeCol},
			{Name: &msgCol},
		},
		Rows: rows,
	}
}

// TestFetchLogs_RealDoctorExample_PostgresLogs confirms FetchLogs also
// covers CapabilityDatabase services using Postgres (doctor's own "db"
// service), querying PGSQLServerLogs against the shared Postgres
// Flexible Server's own resource ID -- mirroring the container-log
// test above's own real-boundary discipline.
func TestFetchLogs_RealDoctorExample_PostgresLogs(t *testing.T) {
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

	// "prod-doctor-pg" is exactly what managed.go's own
	// serverName := getName("pg") would produce for this env/app
	// combination -- inferDatabasesAzure creates one shared Postgres
	// server per app, not one per service.
	resourceID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod/providers/Microsoft.DBforPostgreSQL/flexibleServers/prod-doctor-pg"
	t1 := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)

	client := &fakeLogsClient{
		tables: map[string][]*azquery.Table{
			resourceID: {
				pgLogsTable(azquery.Row{t1, "connection authorized"}),
			},
		},
	}

	events, err := FetchLogs(context.Background(), client, "00000000-0000-0000-0000-000000000000", app, &env, []string{"db"}, time.Time{}, 200)
	if err != nil {
		t.Fatalf("FetchLogs failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	if events[0].Message != "connection authorized" {
		t.Errorf("Message = %q, want %q", events[0].Message, "connection authorized")
	}
	if events[0].Service != "db" {
		t.Errorf("Service = %q, want db", events[0].Service)
	}

	if len(client.seenResourceIDs) != 1 || client.seenResourceIDs[0] != resourceID {
		t.Errorf("QueryResource called with resource IDs %v, want exactly [%s]", client.seenResourceIDs, resourceID)
	}
}

// TestFetchLogs_MySQLDatabaseIsSkipped confirms MySQL/MariaDB database
// services are silently skipped, not queried and not an error --
// MySQL Flexible Server logging is deferred to a follow-up (see
// FetchLogs's own doc comment for why: it needs its own server
// parameters turned on before there's anything to export).
func TestFetchLogs_MySQLDatabaseIsSkipped(t *testing.T) {
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

	var dbService string
	for i := range app.Services {
		if app.Services[i].Capability == models.CapabilityDatabase {
			dbService = app.Services[i].Name
			break
		}
	}
	if dbService == "" {
		t.Fatal("expected at least one database-capability service in nginx-flask-mysql")
	}

	client := &fakeLogsClient{tables: map[string][]*azquery.Table{}}

	events, err := FetchLogs(context.Background(), client, "00000000-0000-0000-0000-000000000000", app, &env, []string{dbService}, time.Time{}, 200)
	if err != nil {
		t.Fatalf("FetchLogs failed: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected no events for a MySQL database service, got %d: %+v", len(events), events)
	}
	if len(client.seenResourceIDs) != 0 {
		t.Errorf("expected no QueryResource calls for a MySQL database service, got %v", client.seenResourceIDs)
	}
}

// missingLogTableErrorBody is a trimmed but real copy of the response
// body Log Analytics actually returned in a live smoke-test failure
// (2026-08-16, francecentral, a brand-new workspace whose
// ContainerAppConsoleLogs_CL custom log table hadn't been created yet)
// -- see isMissingLogTable's own doc comment in logs.go for the full
// story. Used to build a real *azcore.ResponseError via
// runtime.NewResponseError, the same constructor the SDK itself uses, so
// this test exercises isMissingLogTable against the exact same
// Error()-rendered text the real failure did, not a hand-simplified
// stand-in that might not match how *ResponseError actually formats a
// body with a nested innererror.innererror.
const missingLogTableErrorBody = `{
  "error": {
    "message": "The request had some invalid properties",
    "code": "BadArgumentError",
    "correlationId": "616e3a05-c697-4ad3-943a-17fdb3d4cce5",
    "innererror": {
      "code": "SemanticError",
      "message": "A semantic error occurred.",
      "innererror": {
        "code": "SEM0100",
        "message": "'order' operator: Failed to resolve table or column expression named 'ContainerAppConsoleLogs_CL'"
      }
    }
  }
}`

// missingLogTableError builds a real *azcore.ResponseError matching
// missingLogTableErrorBody, for fakeLogsClient.errs to return.
func missingLogTableError() error {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte(missingLogTableErrorBody))),
		Request:    &http.Request{Method: "POST", URL: &url.URL{Scheme: "https", Host: "api.loganalytics.io", Path: "/v1/query"}},
	}
	return runtime.NewResponseError(resp)
}

// TestIsMissingLogTable_MatchesRealAzureFailure confirms isMissingLogTable
// recognizes the exact error shape a real Log Analytics "table doesn't
// exist yet" response produces -- a regression test for the live
// smoke-test failure this function was added to fix.
func TestIsMissingLogTable_MatchesRealAzureFailure(t *testing.T) {
	t.Parallel()
	if !isMissingLogTable(missingLogTableError()) {
		t.Error("expected isMissingLogTable to recognize the real missing-table error shape")
	}
}

// TestIsMissingLogTable_DoesNotMatchOtherBadArgumentErrors confirms a
// different BadArgumentError (a genuine KQL problem, not a missing
// table) is NOT swallowed -- isMissingLogTable's own doc comment
// explains why matching on ErrorCode alone would be too broad.
func TestIsMissingLogTable_DoesNotMatchOtherBadArgumentErrors(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Header:     http.Header{},
		Body: io.NopCloser(bytes.NewReader([]byte(`{
  "error": {
    "message": "The request had some invalid properties",
    "code": "BadArgumentError",
    "innererror": {
      "code": "SyntaxError",
      "message": "Query could not be parsed at 'foo': syntax error"
    }
  }
}`))),
		Request: &http.Request{Method: "POST", URL: &url.URL{Scheme: "https", Host: "api.loganalytics.io", Path: "/v1/query"}},
	}
	if isMissingLogTable(runtime.NewResponseError(resp)) {
		t.Error("expected isMissingLogTable to reject an unrelated BadArgumentError")
	}
}

// TestIsMissingLogTable_DoesNotMatchNonResponseErrors confirms a plain
// Go error (not an *azcore.ResponseError at all) is never treated as a
// missing table.
func TestIsMissingLogTable_DoesNotMatchNonResponseErrors(t *testing.T) {
	t.Parallel()
	if isMissingLogTable(context.DeadlineExceeded) {
		t.Error("expected isMissingLogTable to reject a non-ResponseError")
	}
}

// TestFetchLogs_TreatsMissingLogTableAsZeroEvents confirms FetchLogs
// itself (not just isMissingLogTable in isolation) returns zero events
// and no error when Log Analytics rejects a query for a not-yet-created
// custom log table -- the actual behavior scripts/smoke-test.sh's own
// retry loop depends on: before this fix, this same error aborted
// FetchLogs entirely with a hard error on every single retry, since the
// table's absence never resolved within any bounded retry window.
func TestFetchLogs_TreatsMissingLogTableAsZeroEvents(t *testing.T) {
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

	resourceID := containerAppResourceID("00000000-0000-0000-0000-000000000000", env.ResourceGroupName, "prod-hello-web")
	client := &fakeLogsClient{errs: map[string]error{resourceID: missingLogTableError()}}

	events, err := FetchLogs(context.Background(), client, "00000000-0000-0000-0000-000000000000", app, &env, nil, time.Time{}, 200)
	if err != nil {
		t.Fatalf("expected FetchLogs to treat a missing log table as zero events, not an error, got: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected no events, got %d: %+v", len(events), events)
	}
}
