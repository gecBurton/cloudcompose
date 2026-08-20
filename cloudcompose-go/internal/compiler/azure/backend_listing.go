// Package azure contains Azure-specific inference and Terraform
// generation. This file adds a separate concern, mirroring
// internal/compiler/aws/backend_listing.go: listing every app's own
// state blob under an environment's own backend-key prefix, the
// mechanism environment teardown's dependent-app safety check depends
// on to refuse (by default) tearing down an environment other apps
// still depend on. See docs/multi-user-state.md's "Safe environment
// teardown" section.
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
// container.ListBlobsFlatResponse ListDependentApps needs, decoupled
// from the real SDK's own generic Pager[T] type (which has no
// interface ListDependentApps could otherwise substitute a fake for in
// tests -- see blobLister's own doc comment).
type blobPage struct {
	names      []string
	nextMarker *string
}

// blobLister is the minimal listing operation ListDependentApps needs,
// abstracted away from azblob's own concrete *runtime.Pager[T] (which,
// being a generic struct rather than an interface, can't be
// implemented by a fake the way aws/backend_listing.go's s3Lister
// interface can). realBlobLister below adapts a real
// *container.Client to this interface; tests substitute their own
// implementation instead.
type blobLister interface {
	ListPage(ctx context.Context, prefix string, marker *string) (blobPage, error)
}

// realBlobLister adapts a real *container.Client to blobLister,
// wrapping azblob's own NewListBlobsFlatPager one page at a time rather
// than driving the whole pager internally, so ListDependentApps' own
// pagination loop (mirroring aws.ListDependentApps' shape exactly) is
// identical across both clouds.
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
// ListDependentApps needs from the ambient credential chain, mirroring
// azure.NewAzureClients' own rationale (status.go) exactly.
//
// containerURL is the full container URL
// (https://{account}.blob.core.windows.net/{container}) -- callers
// already have storage_account_name/container_name from
// env.Backend.Azure (see docs/multi-user-state.md), so this takes the
// assembled URL rather than the two parts separately, keeping URL
// construction in one place (cmd/cloudcompose's own env-destroy
// command).
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

// ErrBackendListPermissionDenied mirrors
// aws.ErrBackendListPermissionDenied exactly -- see its own doc comment
// for the full rationale. Azure's own equivalent of S3's AccessDenied
// is an HTTP 403 response (confirmed against azcore.ResponseError's own
// StatusCode field, the same signal status.go's isNotFound already uses
// for 404 specifically).
var ErrBackendListPermissionDenied = errors.New("permission denied listing backend state objects")

// ListDependentApps lists every project name with its own state blob
// under envName's own apps/ prefix in the container client points at,
// mirroring aws.ListDependentApps' behavior and doc comment exactly --
// see there for the full rationale (sorted output, no need to open any
// app's state, etc).
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
