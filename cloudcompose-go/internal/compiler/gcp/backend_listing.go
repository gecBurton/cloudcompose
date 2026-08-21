// Package gcp contains GCP-specific inference and Terraform generation.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"cloud.google.com/go/storage"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
)

// objectPage is one page of an object listing.
type objectPage struct {
	names []string
}

// objectLister is the minimal listing operation ListDependentApps needs.
// Callers must defer client.Close() after a successful NewObjectLister call.
type objectLister interface {
	ListPage(ctx context.Context, prefix string) (objectPage, error)
	Close() error
}

// realObjectLister adapts a real *storage.Client/BucketHandle to objectLister.
type realObjectLister struct {
	client *storage.Client
	bucket *storage.BucketHandle
}

func (r *realObjectLister) ListPage(ctx context.Context, prefix string) (objectPage, error) {
	it := r.bucket.Objects(ctx, &storage.Query{Prefix: prefix})
	var page objectPage
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			return page, nil
		}
		if err != nil {
			return objectPage{}, err
		}
		page.names = append(page.names, attrs.Name)
	}
}

func (r *realObjectLister) Close() error {
	return r.client.Close()
}

// NewObjectLister builds a GCS listing client using the ambient
// credential chain (Application Default Credentials). Callers must
// defer client.Close() after a successful call.
func NewObjectLister(ctx context.Context, bucket string) (objectLister, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}
	return &realObjectLister{client: client, bucket: client.Bucket(bucket)}, nil
}

// ErrBackendListPermissionDenied is returned when listing backend state
// objects fails due to a permission error (GCS returns HTTP 403).
var ErrBackendListPermissionDenied = errors.New("permission denied listing backend state objects")

// ListDependentApps lists every project name with its own state object
// under envName's apps/ prefix in the bucket client points at.
func ListDependentApps(ctx context.Context, client objectLister, envName string) ([]string, error) {
	prefix := shared.BackendAppsPrefix(envName)

	page, err := client.ListPage(ctx, prefix)
	if err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == 403 {
			return nil, fmt.Errorf("%w: %s", ErrBackendListPermissionDenied, err)
		}
		return nil, fmt.Errorf("list objects under %s: %w", prefix, err)
	}

	var projectNames []string
	for _, name := range page.names {
		if projectName, ok := shared.ProjectNameFromAppKey(envName, name); ok {
			projectNames = append(projectNames, projectName)
		}
	}

	sort.Strings(projectNames)
	return projectNames, nil
}
