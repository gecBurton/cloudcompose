// Package azure contains Azure-specific inference and Terraform
// generation. This file lists every app's state blob under an
// environment's backend-key prefix, used by environment teardown's
// dependent-app safety check.
package azure

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
)

// blobPage is one page of a blob listing: the subset of
// container.ListBlobsFlatResponse ListDependentApps needs.
type blobPage struct {
	names      []string
	nextMarker *string
}

// blobLister is the minimal listing operation ListDependentApps needs,
// abstracted away from azblob's concrete *runtime.Pager[T] so tests can
// substitute a fake implementation.
type blobLister interface {
	ListPage(ctx context.Context, prefix string, marker *string) (blobPage, error)
}

// realBlobLister adapts a real *container.Client to blobLister, wrapping
// azblob's NewListBlobsFlatPager one page at a time.
type realBlobLister struct {
	client *container.Client
}

func (r *realBlobLister) ListPage(ctx context.Context, prefix string, marker *string) (blobPage, error) {
	pager := r.client.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Prefix: &prefix,
		Marker: marker,
	})
	resp, err := pager.NextPage(ctx)
	if err != nil {
		return blobPage{}, err
	}

	var page blobPage
	if resp.Segment != nil {
		for _, item := range resp.Segment.BlobItems {
			if item.Name != nil {
				page.names = append(page.names, *item.Name)
			}
		}
	}
	page.nextMarker = resp.NextMarker
	return page, nil
}

// NewBlobContainerClient builds the real blob listing client
// ListDependentApps needs from the ambient credential chain.
//
// containerURL is the full container URL
// (https://{account}.blob.core.windows.net/{container}).
func NewBlobContainerClient(containerURL string) (blobLister, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("load Azure credentials: %w", err)
	}
	client, err := container.NewClient(containerURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create blob container client: %w", err)
	}
	return &realBlobLister{client: client}, nil
}

// ErrBackendListPermissionDenied signals that listing the backend
// container failed due to a permissions error (HTTP 403).
var ErrBackendListPermissionDenied = errors.New("permission denied listing backend state objects")

// ListDependentApps lists every project name with its own state blob
// under envName's apps/ prefix in the container client points at.
func ListDependentApps(ctx context.Context, client blobLister, envName string) ([]string, error) {
	prefix := shared.BackendAppsPrefix(envName)

	var projectNames []string
	var marker *string
	for {
		page, err := client.ListPage(ctx, prefix, marker)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == 403 {
				return nil, fmt.Errorf("%w: %s", ErrBackendListPermissionDenied, err)
			}
			return nil, fmt.Errorf("list blobs under %s: %w", prefix, err)
		}

		for _, name := range page.names {
			if projectName, ok := shared.ProjectNameFromAppKey(envName, name); ok {
				projectNames = append(projectNames, projectName)
			}
		}

		if page.nextMarker == nil || *page.nextMarker == "" {
			break
		}
		marker = page.nextMarker
	}

	sort.Strings(projectNames)
	return projectNames, nil
}
