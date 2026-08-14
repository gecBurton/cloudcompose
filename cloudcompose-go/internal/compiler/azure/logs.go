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

// FetchLogs returns recent log events for the given container services
// (or every container service in app if services is empty, matching
// `docker compose logs`'s own no-argument behavior), across the given
// resource group/subscription's Container Apps.
//
// Scheduled services are skipped, mirroring FetchStatus's own choice:
// Container App Jobs have their own execution-log model, not the
// steady-state console-log stream this command targets.
//
// One QueryResource call per service (not one shared query across all
// of them): each call targets that service's own Container App resource
// ID, letting Log Analytics resolve which workspace to search itself --
// avoiding a second ARM round-trip to resolve the workspace's own GUID
// customerId just to use QueryWorkspace's single-query-many-services
// shape instead. A service with no logs yet (never deployed) returns an
// empty table, not an error -- the same "not found is not a failure"
// principle status.go's ServiceStatus.Found follows.
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
		if service.Capability != models.CapabilityContainer {
			continue
		}
		if service.Schedule != nil {
			continue
		}
		if len(wanted) > 0 && !wanted[service.Name] {
			continue
		}

		resourceID := containerAppResourceID(subscriptionID, env.ResourceGroupName, getName(service.Name))
		query := fmt.Sprintf(
			"ContainerAppConsoleLogs_CL | order by TimeGenerated desc | take %d | project TimeGenerated, Log_s",
			limit,
		)
		body := azquery.Body{Query: &query}
		if !since.IsZero() {
			body.Timespan = to(azquery.NewTimeInterval(since, time.Now().UTC()))
		}

		resp, err := client.QueryResource(ctx, resourceID, body, nil)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("query logs for %s: %w", service.Name, err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("query logs for %s: %s", service.Name, errorInfoMessage(resp.Error))
		}

		for _, table := range resp.Tables {
			timeIdx, msgIdx := -1, -1
			for i, col := range table.Columns {
				if col.Name == nil {
					continue
				}
				switch *col.Name {
				case "TimeGenerated":
					timeIdx = i
				case "Log_s":
					msgIdx = i
				}
			}
			if timeIdx == -1 || msgIdx == -1 {
				continue
			}
			for _, row := range table.Rows {
				events = append(events, LogEvent{
					Service:   service.Name,
					Timestamp: rowTime(row[timeIdx]),
					Message:   rowString(row[msgIdx]),
				})
			}
		}
	}

	sort.Slice(events, func(i, j int) bool { return events[i].Timestamp.Before(events[j].Timestamp) })
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
