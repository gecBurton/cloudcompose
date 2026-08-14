package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// fakeCloudWatchLogsClient is a minimal in-memory stand-in for
// *cloudwatchlogs.Client, keyed by log group name, mirroring
// status_test.go's fakeECSClient/fakeELBClient rationale.
type fakeCloudWatchLogsClient struct {
	events map[string][]cwltypes.FilteredLogEvent
	// seenGroups records every LogGroupName DescribeServices was called
	// with, so tests can assert FetchLogs queried exactly the log group
	// names compute.go's own naming would have created.
	seenGroups []string
}

func (f *fakeCloudWatchLogsClient) FilterLogEvents(ctx context.Context, params *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error) {
	logGroup := aws.ToString(params.LogGroupName)
	f.seenGroups = append(f.seenGroups, logGroup)
	events, ok := f.events[logGroup]
	if !ok {
		return nil, &cwltypes.ResourceNotFoundException{Message: aws.String("log group not found")}
	}
	return &cloudwatchlogs.FilterLogEventsOutput{Events: events}, nil
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
	env := fullMockProdEnv()

	// "/ecs/prod-hello-web" is exactly what compute.go's own
	// logGroup.Name = "/ecs/" + getName(service.Name) would produce for
	// this env/app/service combination.
	client := &fakeCloudWatchLogsClient{
		events: map[string][]cwltypes.FilteredLogEvent{
			"/ecs/prod-hello-web": {
				{Timestamp: aws.Int64(2000), Message: aws.String("second")},
				{Timestamp: aws.Int64(1000), Message: aws.String("first")},
			},
		},
	}

	events, err := FetchLogs(context.Background(), client, app, &env, nil, 0, 200)
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

	if len(client.seenGroups) != 1 || client.seenGroups[0] != "/ecs/prod-hello-web" {
		t.Errorf("FilterLogEvents called with log groups %v, want exactly [/ecs/prod-hello-web]", client.seenGroups)
	}
}

// TestFetchLogs_NotYetDeployedIsNotAnError confirms a service whose log
// group doesn't exist yet (never deployed, or deployed under a since-
// changed name) is silently skipped rather than failing the whole
// command -- the same principle status.go's ServiceStatus.Found follows.
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
	env := fullMockProdEnv()

	client := &fakeCloudWatchLogsClient{events: map[string][]cwltypes.FilteredLogEvent{}}

	events, err := FetchLogs(context.Background(), client, app, &env, nil, 0, 200)
	if err != nil {
		t.Fatalf("FetchLogs failed: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected no events, got %d", len(events))
	}
}

// TestFetchLogs_FiltersToNamedServices confirms passing explicit
// service names (like `cloudcompose logs web`) only queries those
// services' log groups, not every container service in the app.
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
	env := fullMockProdEnv()

	// Find one real container-capability service name to filter to.
	var containerService string
	for _, s := range app.Services {
		if s.Capability == models.CapabilityContainer {
			containerService = s.Name
			break
		}
	}
	if containerService == "" {
		t.Fatal("expected at least one container-capability service in nginx-flask-mysql")
	}

	client := &fakeCloudWatchLogsClient{events: map[string][]cwltypes.FilteredLogEvent{}}

	if _, err := FetchLogs(context.Background(), client, app, &env, []string{containerService}, 0, 200); err != nil {
		t.Fatalf("FetchLogs failed: %v", err)
	}

	if len(client.seenGroups) != 1 {
		t.Fatalf("expected exactly 1 log group queried when filtering to one service, got %d: %v", len(client.seenGroups), client.seenGroups)
	}
}

// TestFetchLogs_RealNginxFlaskMysqlExample_DatabaseLogs confirms
// FetchLogs also covers CapabilityDatabase services (mariadb's "db"
// service in this real example), querying every RDS log group its
// engine exports and merging them under one Service name, mirroring
// the container-log test above's own real-boundary discipline.
func TestFetchLogs_RealNginxFlaskMysqlExample_DatabaseLogs(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/nginx-flask-mysql/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "nginx-flask-mysql")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := fullMockProdEnv()

	// mariadb's own RDSLogExports list is
	// ["audit", "error", "general", "slowquery"] -- "/aws/rds/instance/
	// prod-nginx-flask-mysql-db/<type>" is exactly what
	// managed.go's inferDatabase + this package's own naming would
	// produce for the "db" service in this env/app combination.
	client := &fakeCloudWatchLogsClient{
		events: map[string][]cwltypes.FilteredLogEvent{
			"/aws/rds/instance/prod-nginx-flask-mysql-db/error": {
				{Timestamp: aws.Int64(2000), Message: aws.String("connection refused")},
			},
			"/aws/rds/instance/prod-nginx-flask-mysql-db/slowquery": {
				{Timestamp: aws.Int64(1000), Message: aws.String("slow query detected")},
			},
		},
	}

	events, err := FetchLogs(context.Background(), client, app, &env, []string{"db"}, 0, 200)
	if err != nil {
		t.Fatalf("FetchLogs failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (one per log type), got %d: %+v", len(events), events)
	}
	// Sorted chronologically across log types, not grouped by type.
	if events[0].Message != "slow query detected" || events[1].Message != "connection refused" {
		t.Errorf("events not sorted by timestamp across log types: %+v", events)
	}
	for _, e := range events {
		if e.Service != "db" {
			t.Errorf("expected Service = db for every event regardless of log type, got %q", e.Service)
		}
	}

	// mariadb's RDSLogExports has 4 entries; audit/general have no fake
	// data registered, so they should still be queried (and correctly
	// treated as "not found", not an error) alongside error/slowquery.
	if len(client.seenGroups) != 4 {
		t.Errorf("expected all 4 of mariadb's log types to be queried, got %d: %v", len(client.seenGroups), client.seenGroups)
	}
}
