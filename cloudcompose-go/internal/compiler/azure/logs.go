// This file adds another concern to the azure package, alongside
// status.go's live `ps` support: live logs ("cloudcompose logs"). Same
// rationale as status.go -- deliberately independent of Terraform
// state/output, using the same env.Name-app.Name-service.Name formula
// InferAzure's own getName closure uses to name each Container App in
// the first place.
//
// Unlike AWS, Container Apps has no per-service log group: every
// Container App in a Cloud Compose Environment logs into one shared
// Log Analytics workspace (see appsubnets.go's
// ContainerAppEnvironment.LogAnalyticsWorkspaceID, set unconditionally
// to env.LogAnalyticsWorkspaceID for every app), distinguished only by
// the ContainerAppName_s column of the ContainerAppConsoleLogs_CL
// table -- there's no separate resource to point at the way AWS's
// "/ecs/"+getName(service.Name) log group is. Rather than resolving
// the workspace's own GUID customerId (a separate ARM property,
// requiring either a new dependency or a new field on AzureEnvironment
// to cache it) to use LogsClient.QueryWorkspace, this file uses
// LogsClient.QueryResource against each Container App's own computed
// resource ID instead: Log Analytics auto-discovers which workspace a
// resource logs to, so the ARM resource ID `ps` already knows how to
// build (status.go's azureName, wrapped in the standard
// /subscriptions/.../containerApps/... shape) is enough on its own.
package azure

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
	"github.com/gecburton/cloudcompose/internal/models"
)

// LogEvent is one line of Azure `cloudcompose logs` output, mirroring
// aws.LogEvent's shape.
type LogEvent struct {
	Service   string
	Timestamp time.Time
	Message   string
}

// logsClient is the subset of *azquery.LogsClient that FetchLogs needs,
// mirroring status.go's containerAppsClient/revisionsClient rationale:
// letting tests substitute a fake without a real Azure call.
type logsClient interface {
	QueryResource(ctx context.Context, resourceID string, body azquery.Body, options *azquery.LogsClientQueryResourceOptions) (azquery.LogsClientQueryResourceResponse, error)
}

// NewLogsClient builds the real Azure Monitor Logs client FetchLogs
// needs, from the same ambient credential chain NewAzureClients already
// uses (see its own doc comment) -- kept as a separate constructor for
// the same reason aws.NewCloudWatchLogsClient is separate from
// aws.NewAWSClients: `ps` doesn't gain an unused dependency on this
// package's logs client.
//
// Unlike NewAzureClients, this needs no subscriptionID: azquery.LogsClient
// is a data-plane client (Log Analytics' own query endpoint, not an ARM
// management-plane one), so the subscription is only ever encoded in
// the resourceID string passed to QueryResource, not in the client
// itself.
func NewLogsClient() (logsClient, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("load Azure credentials: %w", err)
	}
	return azquery.NewLogsClient(cred, nil)
}

// FetchLogs returns recent log events for the given services (or every
// container/database service in app if services is empty, matching
// `docker compose logs`'s own no-argument behavior), across the given
// resource group/subscription.
//
// Covers two service capabilities, each with its own resource type and
// query shape:
//
//   - CapabilityContainer: scheduled services are skipped (Container
//     App Jobs have their own execution-log model, not the
//     steady-state console-log stream this targets), and queries
//     ContainerAppConsoleLogs_CL's Log_s column.
//   - CapabilityDatabase, Postgres only (MySQL/MariaDB deferred to a
//     follow-up -- MySQL Flexible Server needs its own
//     audit_log_enabled/slow_query_log server parameters turned on
//     before there's anything to export at all, unlike Postgres, which
//     logs its own error/notice output by default once the diagnostic
//     setting managed.go's inferDatabasesAzure creates routes it to
//     the workspace): queries PGSQLServerLogs, a resource-specific
//     table (not the legacy shared AzureDiagnostics table other
//     Azure services still use) with the same TimeGenerated/Message
//     column names ContainerAppConsoleLogs_CL happens to also use,
//     confirmed against Microsoft's own PGSQLServerLogs table
//     reference.
//
// One QueryResource call per service (not one shared query across all
// of them), for both capabilities: each call targets that service's own
// resource ID (a Container App or a PostgreSQL Flexible Server), letting
// Log Analytics resolve which workspace to search itself -- avoiding a
// second ARM round-trip to resolve the workspace's own GUID customerId
// just to use QueryWorkspace's single-query-many-services shape instead.
// A service with no logs yet (never deployed) returns an empty table,
// not an error -- the same "not found is not a failure" principle
// status.go's ServiceStatus.Found follows.
func FetchLogs(ctx context.Context, client logsClient, subscriptionID string, app *models.Application, env *models.AzureEnvironment, services []string, since time.Time, limit int32) ([]LogEvent, error) {
	getName := func(resourceName string) string {
		return env.Name + "-" + app.Name + "-" + resourceName
	}

	wanted := make(map[string]bool, len(services))
	for _, s := range services {
		wanted[s] = true
	}

	var events []LogEvent
	for i := range app.Services {
		service := &app.Services[i]
		if len(wanted) > 0 && !wanted[service.Name] {
			continue
		}

		switch service.Capability {
		case models.CapabilityContainer:
			if service.Schedule != nil {
				continue
			}
			resourceID := containerAppResourceID(subscriptionID, env.ResourceGroupName, getName(service.Name))
			query := fmt.Sprintf(
				"ContainerAppConsoleLogs_CL | order by TimeGenerated desc | take %d | project TimeGenerated, Log_s",
				limit,
			)
			serviceEvents, err := fetchLogAnalyticsEvents(ctx, client, service.Name, resourceID, query, since, "Log_s")
			if err != nil {
				return nil, err
			}
			events = append(events, serviceEvents...)

		case models.CapabilityDatabase:
			if isMySQLImage(service.Image) {
				// MySQL/MariaDB Flexible Server logging deferred -- see
				// this function's own doc comment.
				continue
			}
			resourceID := postgresFlexibleServerResourceID(subscriptionID, env.ResourceGroupName, getName("pg"))
			query := fmt.Sprintf(
				"PGSQLServerLogs | order by TimeGenerated desc | take %d | project TimeGenerated, Message",
				limit,
			)
			serviceEvents, err := fetchLogAnalyticsEvents(ctx, client, service.Name, resourceID, query, since, "Message")
			if err != nil {
				return nil, err
			}
			events = append(events, serviceEvents...)
		}
	}

	sort.Slice(events, func(i, j int) bool { return events[i].Timestamp.Before(events[j].Timestamp) })
	return events, nil
}

// fetchLogAnalyticsEvents runs one QueryResource call and converts its
// result into LogEvents tagged with serviceName -- the shared plumbing
// both the container and database branches of FetchLogs build on.
// messageColumn is the log message column's name, since the two
// resource types this file queries happen to use different ones
// (Log_s vs Message) despite both using TimeGenerated for the
// timestamp.
func fetchLogAnalyticsEvents(ctx context.Context, client logsClient, serviceName, resourceID, query string, since time.Time, messageColumn string) ([]LogEvent, error) {
	body := azquery.Body{Query: &query}
	if !since.IsZero() {
		body.Timespan = to(azquery.NewTimeInterval(since, time.Now().UTC()))
	}

	resp, err := client.QueryResource(ctx, resourceID, body, nil)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("query logs for %s: %w", serviceName, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("query logs for %s: %s", serviceName, errorInfoMessage(resp.Error))
	}

	var events []LogEvent
	for _, table := range resp.Tables {
		timeIdx, msgIdx := -1, -1
		for i, col := range table.Columns {
			if col.Name == nil {
				continue
			}
			switch *col.Name {
			case "TimeGenerated":
				timeIdx = i
			case messageColumn:
				msgIdx = i
			}
		}
		if timeIdx == -1 || msgIdx == -1 {
			continue
		}
		for _, row := range table.Rows {
			events = append(events, LogEvent{
				Service:   serviceName,
				Timestamp: rowTime(row[timeIdx]),
				Message:   rowString(row[msgIdx]),
			})
		}
	}
	return events, nil
}

// containerAppResourceID builds the fully-qualified ARM resource ID for
// a Container App -- the shape azquery.LogsClient.QueryResource
// expects, and the same shape confirmed against
// armappcontainers' own example fixtures
// (/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.App/containerApps/{name}).
func containerAppResourceID(subscriptionID, resourceGroupName, containerAppName string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
		subscriptionID, resourceGroupName, containerAppName)
}

// postgresFlexibleServerResourceID builds the fully-qualified ARM
// resource ID for a PostgreSQL Flexible Server, mirroring
// containerAppResourceID's own rationale. serverName is always
// getName("pg") -- managed.go's inferDatabasesAzure creates exactly one
// shared Postgres server per app (serverName := getName("pg")), covering
// every Postgres-backed compose service in that app, not one server per
// service.
func postgresFlexibleServerResourceID(subscriptionID, resourceGroupName, serverName string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s",
		subscriptionID, resourceGroupName, serverName)
}

// errorInfoMessage extracts a human-readable message from an
// azquery.ErrorInfo -- Azure's own "partial failure" signal for a query
// that returned HTTP 200 but still failed (bad KQL, etc.), which err
// itself won't ever surface. ErrorInfo implements the error interface
// itself (its Error() method renders the full raw error payload), so
// this just delegates to that rather than re-deriving a message from
// its own Code field.
func errorInfoMessage(e *azquery.ErrorInfo) string {
	if e == nil {
		return "unknown Log Analytics query error"
	}
	return e.Error()
}

// rowTime and rowString defensively convert a Log Analytics row cell
// (returned as `any` -- see azquery.Row's own doc comment) to the Go
// types FetchLogs needs, tolerating a cell that's nil or an unexpected
// type rather than panicking.
func rowTime(cell any) time.Time {
	switch v := cell.(type) {
	case time.Time:
		return v
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func rowString(cell any) string {
	if s, ok := cell.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", cell)
}

func to[T any](v T) *T { return &v }
