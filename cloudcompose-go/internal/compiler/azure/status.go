// Package azure contains Azure-specific inference and Terraform
// generation. This file adds live status ("cloudcompose ps"), entirely
// independent of the parse/normalize/infer/generate pipeline used
// elsewhere in this package: it never touches Terraform state or output.
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
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// ServiceStatus is one row of `cloudcompose ps` output for Azure.
type ServiceStatus struct {
	// Name is the compose service name (e.g. "web").
	Name string

	// AzureName is the Container App name ps computed and queried Azure
	// for (env.Name-app.Name-service.Name).
	AzureName string

	// Found is false when Azure has no Container App by that name at
	// all. Every other field is meaningless when this is false.
	Found bool

	// ProvisioningState is the Container App's top-level provisioning
	// state (Succeeded/Failed/InProgress/Canceled).
	ProvisioningState string

	// Replicas is the latest revision's running pod count.
	Replicas int32

	// HasIngress is true when the compose service declared a port wired
	// to the Container App's ingress.
	HasIngress bool

	// HealthState is the latest revision's aggregate health
	// (Healthy/Unhealthy/None).
	HealthState string
}

// containerAppsClient is the subset of *armappcontainers.ContainerAppsClient
// that FetchStatus needs, letting tests substitute a fake without a real
// Azure call.
type containerAppsClient interface {
	Get(ctx context.Context, resourceGroupName, containerAppName string, options *armappcontainers.ContainerAppsClientGetOptions) (armappcontainers.ContainerAppsClientGetResponse, error)
}

// revisionsClient is the subset of
// *armappcontainers.ContainerAppsRevisionsClient that FetchStatus needs.
type revisionsClient interface {
	GetRevision(ctx context.Context, resourceGroupName, containerAppName, revisionName string, options *armappcontainers.ContainerAppsRevisionsClientGetRevisionOptions) (armappcontainers.ContainerAppsRevisionsClientGetRevisionResponse, error)
}

// NewAzureClients builds the real Azure SDK clients FetchStatus needs from
// the ambient credential chain (env vars, managed identity, Azure CLI
// login, or workload identity).
//
// subscriptionID is not a field on AzureEnvironment, so callers must supply
// it themselves; SubscriptionIDFromResourceID extracts it from any
// fully-qualified resource ID already on the environment.
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

// SubscriptionIDFromResourceID extracts the subscription ID segment from
// any fully-qualified Azure resource ID
// ("/subscriptions/{id}/resourceGroups/.../providers/...").
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
// container service in app, within the resource group env points at. It
// recomputes each Container App name (env.Name-app.Name-service.Name)
// rather than reading it from Terraform state. Scheduled services become
// Container App Jobs, not Container Apps, and have no steady-state replica
// pool, so they're skipped.
func FetchStatus(ctx context.Context, appsC containerAppsClient, revC revisionsClient, app *models.Application, env *models.AzureEnvironment) ([]ServiceStatus, error) {
	getName := shared.ResourceNamer(env.Name, app.Name)

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

// isNotFound reports whether err is Azure's 404 response, meaning the
// Container App/revision doesn't exist -- expected for a service that's
// never been deployed, not a failure worth surfacing as one.
func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == 404
	}
	return false
}
