package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// LogEvent is one line of Azure `cloudcompose logs` output.
type LogEvent struct {
	Service   string
	Timestamp time.Time
	Message   string
}

// logsClient is the subset of *azquery.LogsClient that FetchLogs needs,
// letting tests substitute a fake without a real Azure call.
type logsClient interface {
	QueryResource(ctx context.Context, resourceID string, body azquery.Body, options *azquery.LogsClientQueryResourceOptions) (azquery.LogsClientQueryResourceResponse, error)
}

// NewLogsClient builds the real Azure Monitor Logs client FetchLogs needs,
// using the ambient credential chain. No subscriptionID is required: it's a
// data-plane client, and the subscription is encoded in the resourceID
// passed to QueryResource instead.
func NewLogsClient() (logsClient, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("load Azure credentials: %w", err)
	}
	return azquery.NewLogsClient(cred, nil)
}

// FetchLogs returns recent log events for the given services (or every
// container/database service in app if services is empty), across the given
// resource group/subscription.
//
// Container services query ContainerAppConsoleLogs_CL (scheduled services
// are skipped, since jobs have their own execution-log model). Database
// services query PGSQLServerLogs; MySQL/MariaDB is not yet supported. A
// service with no logs yet returns an empty result, not an error.
func FetchLogs(ctx context.Context, client logsClient, subscriptionID string, app *models.Application, env *models.AzureEnvironment, services []string, since time.Time, limit int32) ([]LogEvent, error) {
	getName := shared.ResourceNamer(env.Name, app.Name)

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
// result into LogEvents tagged with serviceName. messageColumn is the name
// of the log message column, which varies by resource type.
func fetchLogAnalyticsEvents(ctx context.Context, client logsClient, serviceName, resourceID, query string, since time.Time, messageColumn string) ([]LogEvent, error) {
	body := azquery.Body{Query: &query}
	if !since.IsZero() {
		body.Timespan = to(azquery.NewTimeInterval(since, time.Now().UTC()))
	}

	resp, err := client.QueryResource(ctx, resourceID, body, nil)
	if err != nil {
		if isNotFound(err) || isMissingLogTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("query logs for %s: %w", serviceName, err)
	}
	if resp.Error != nil {
		if strings.Contains(errorInfoMessage(resp.Error), "Failed to resolve table or column expression") {
			return nil, nil
		}
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

// isMissingLogTable reports whether err is Log Analytics rejecting a query
// because a custom log table (e.g. ContainerAppConsoleLogs_CL,
// PGSQLServerLogs) doesn't exist in the workspace yet. These tables are
// created lazily on first ingestion, so a brand-new environment's first
// `cloudcompose logs` call hits this before anything has ever logged;
// treated as "zero events" rather than an error.
//
// Azure returns HTTP 400 with ErrorCode "BadArgumentError" for this case, so
// the error text is also checked for the specific KQL "failed to resolve"
// message to avoid masking genuine query errors that share the same code.
func isMissingLogTable(err error) bool {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	if respErr.StatusCode != http.StatusBadRequest || respErr.ErrorCode != "BadArgumentError" {
		return false
	}
	return strings.Contains(respErr.Error(), "Failed to resolve table or column expression")
}

// containerAppResourceID builds the fully-qualified ARM resource ID for a
// Container App.
func containerAppResourceID(subscriptionID, resourceGroupName, containerAppName string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
		subscriptionID, resourceGroupName, containerAppName)
}

// postgresFlexibleServerResourceID builds the fully-qualified ARM resource
// ID for a PostgreSQL Flexible Server. serverName is always getName("pg"):
// there is one shared Postgres server per app.
func postgresFlexibleServerResourceID(subscriptionID, resourceGroupName, serverName string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s",
		subscriptionID, resourceGroupName, serverName)
}

// errorInfoMessage extracts a human-readable message from an
// azquery.ErrorInfo, Azure's "partial failure" signal for a query that
// returned HTTP 200 but still failed.
func errorInfoMessage(e *azquery.ErrorInfo) string {
	if e == nil {
		return "unknown Log Analytics query error"
	}
	return e.Error()
}

// rowTime and rowString convert a Log Analytics row cell (returned as
// `any`) to the Go types FetchLogs needs, tolerating nil or unexpected types
// rather than panicking.
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
