// Package azure contains Azure-specific inference and Terraform
// generation. This file adds a separate concern, mirroring
// internal/compiler/aws/status.go: live status ("cloudcompose ps"),
// entirely independent of the parse/normalize/infer/generate pipeline
// used everywhere else in this package. It never touches Terraform
// state or output -- see FetchStatus's own doc comment for why.
package azure

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
	"github.com/gecburton/cloudcompose/internal/models"
)

// ServiceStatus is one row of `cloudcompose ps` output for Azure,
// mirroring aws.ServiceStatus's shape as closely as Container Apps'
// own status model allows -- see that struct's own doc comment for the
// AWS equivalents of each field below.
type ServiceStatus struct {
	// Name is the compose service name (e.g. "web").
	Name string

	// AzureName is the Container App name ps computed and queried
	// Azure for (env.Name-app.Name-service.Name, the same formula
	// InferAzure's own getName closure uses -- see FetchStatus's own
	// doc comment for why this is recomputed rather than read from
	// anywhere).
	AzureName string

	// Found is false when Azure has no Container App by that name at
	// all: not yet deployed, or deployed under a name that no longer
	// matches. Every other field is meaningless when this is false.
	Found bool

	// ProvisioningState is the Container App's own top-level
	// provisioning state (Succeeded/Failed/InProgress/Canceled),
	// verbatim from the ContainerApps Get call.
	ProvisioningState string

	// Replicas is the latest revision's own running pod count --
	// Container Apps has no separate desired/running/pending triad
	// the way ECS does; a single Replicas count against the compose
	// file's own min/max scale settings is the closest equivalent.
	Replicas int32

	// HasIngress is true when the compose service declared a port
	// that InferAzure would have wired to the Container App's own
	// ingress -- see compute.go's ContainerAppIngress construction.
	// Determines whether HealthState below is meaningful: a service
	// with no ingress still runs, but Container Apps' own
	// per-revision HealthState still reports something (it isn't
	// exclusively an ingress-health signal the way AWS's target-group
	// health is), so this field mainly controls whether ps bothers
	// showing it.
	HasIngress bool

	// HealthState is the latest revision's own aggregate health
	// (Healthy/Unhealthy/None) -- Container Apps has no separate
	// target-group resource the way AWS's ALB does (see
	// docs/azure-aws-parity-todo.md's note that Azure has no shared
	// ingress/ALB equivalent), so this per-revision health state is
	// the nearest analogue to AWS's target-group health.
	HealthState string
}

// containerAppsClient is the subset of *armappcontainers.ContainerAppsClient
// that FetchStatus needs, letting tests substitute a fake without a
// real Azure call.
type containerAppsClient interface {
	Get(ctx context.Context, resourceGroupName, containerAppName string, options *armappcontainers.ContainerAppsClientGetOptions) (armappcontainers.ContainerAppsClientGetResponse, error)
}

// revisionsClient is the subset of
// *armappcontainers.ContainerAppsRevisionsClient that FetchStatus
// needs, mirroring containerAppsClient's rationale.
type revisionsClient interface {
	GetRevision(ctx context.Context, resourceGroupName, containerAppName, revisionName string, options *armappcontainers.ContainerAppsRevisionsClientGetRevisionOptions) (armappcontainers.ContainerAppsRevisionsClientGetRevisionResponse, error)
}

// NewAzureClients builds the real Azure SDK clients FetchStatus needs
// from the ambient credential chain (environment variables, managed
// identity, Azure CLI login, or workload identity -- whatever
// azidentity.NewDefaultAzureCredential already knows how to find; the
// same credential surface CI and local `az login` already populate, so
// ps needs no new auth code of its own, mirroring aws.NewAWSClients'
// own rationale).
//
// subscriptionID is not a field on AzureEnvironment (no cloudcompose
// resource creation needs one directly -- resource_group_name and
// location cover every call site in this package), so callers must
// supply it themselves; SubscriptionIDFromResourceID extracts it from
// any fully-qualified resource ID already on the environment (e.g.
// LogAnalyticsWorkspaceID, always populated) rather than requiring a
// new environment.yaml field just for `ps`.
func NewAzureClients(subscriptionID string) (containerAppsClient, revisionsClient, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("load Azure credentials: %w", err)
	}
	appsClient, err := armappcontainers.NewContainerAppsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create Container Apps client: %w", err)
	}
	revClient, err := armappcontainers.NewContainerAppsRevisionsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create Container Apps revisions client: %w", err)
	}
	return appsClient, revClient, nil
}

// SubscriptionIDFromResourceID extracts the subscription ID segment
// from any fully-qualified Azure resource ID
// ("/subscriptions/{id}/resourceGroups/.../providers/...") --
// AzureEnvironment always has at least LogAnalyticsWorkspaceID
// populated in this shape (see LoadAzureEnvironment), so `ps` never
// needs a dedicated SubscriptionID field of its own.
func SubscriptionIDFromResourceID(resourceID string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(resourceID, "/"), "/")
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("no /subscriptions/ segment found in resource ID %q", resourceID)
}

// FetchStatus queries live Container App status for every non-scheduled
// container service in app, within the resource group env points at.
//
// Deliberately independent of Terraform state/output, mirroring
// aws.FetchStatus's own rationale: env.Name and app.Name are exactly
// the same two inputs InferAzure's own getName closure combines
// (env.Name + "-" + app.Name + "-" + resourceName, infer.go) to name
// every Container App/Job cloudcompose creates, so ps recomputes the
// name itself rather than reading it back from anywhere.
//
// Scheduled services (Schedule != nil) become Container App Jobs, not
// Container Apps (see compute.go's inferScheduledJobs/inferContainerApps
// split) -- Jobs have no steady-state replica pool, only
// per-execution run history, so ps skips them entirely, matching
// aws.FetchStatus's own choice to skip EventBridge-scheduled ECS tasks
// for the same reason.
func FetchStatus(ctx context.Context, appsC containerAppsClient, revC revisionsClient, app *models.Application, env *models.AzureEnvironment) ([]ServiceStatus, error) {
	getName := func(resourceName string) string {
		return env.Name + "-" + app.Name + "-" + resourceName
	}

	var result []ServiceStatus
	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer {
			continue
		}
		if service.Schedule != nil {
			continue
		}

		azureName := getName(service.Name)
		status := ServiceStatus{
			Name:       service.Name,
			AzureName:  azureName,
			HasIngress: service.Ingress != nil,
		}

		appResp, err := appsC.Get(ctx, env.ResourceGroupName, azureName, nil)
		if err != nil {
			if isNotFound(err) {
				result = append(result, status)
				continue
			}
			return nil, fmt.Errorf("get Container App %s: %w", azureName, err)
		}
		status.Found = true
		if appResp.Properties != nil && appResp.Properties.ProvisioningState != nil {
			status.ProvisioningState = string(*appResp.Properties.ProvisioningState)
		}

		var latestRevision string
		if appResp.Properties != nil && appResp.Properties.LatestRevisionName != nil {
			latestRevision = *appResp.Properties.LatestRevisionName
		}
		if latestRevision != "" {
			revResp, err := revC.GetRevision(ctx, env.ResourceGroupName, azureName, latestRevision, nil)
			if err != nil {
				if !isNotFound(err) {
					return nil, fmt.Errorf("get revision %s for %s: %w", latestRevision, azureName, err)
				}
			} else if revResp.Properties != nil {
				if revResp.Properties.Replicas != nil {
					status.Replicas = *revResp.Properties.Replicas
				}
				if revResp.Properties.HealthState != nil {
					status.HealthState = string(*revResp.Properties.HealthState)
				}
			}
		}

		result = append(result, status)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// isNotFound reports whether err is Azure's own 404 response, meaning
// the Container App/revision itself doesn't exist -- expected for a
// service that's never been deployed, not a real failure `ps` should
// surface as one, mirroring aws.isLogGroupNotFound's rationale.
func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == 404
	}
	return false
}
