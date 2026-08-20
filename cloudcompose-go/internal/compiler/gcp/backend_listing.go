// Package gcp contains GCP-specific inference and Terraform generation.
// This file adds a separate concern, mirroring
// internal/compiler/aws/backend_listing.go and
// internal/compiler/azure/backend_listing.go: listing every app's own
// state object under an environment's own backend-key prefix, the
// mechanism environment teardown's dependent-app safety check depends
// on to refuse (by default) tearing down an environment other apps
// still depend on. See docs/multi-user-state.md's "Safe environment
// teardown" section.
//
// GCP has no dedicated status.go/logs.go equivalent this file could
// otherwise mirror the existing client-construction pattern from --
// this is the first GCP SDK dependency this codebase has (AGENTS.md
// already notes GCP's own support is deliberately the least-verified
// of the three clouds), so NewObjectLister below is written from
// scratch rather than adapted from an existing NewAWSClients/
// NewAzureClients.
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

// objectPage is one page of an object listing: the subset of iterating
// *storage.ObjectIterator ListDependentApps needs, decoupled from the
// real SDK's own concrete iterator type (which has no interface
// ListDependentApps could otherwise substitute a fake for in tests --
// mirroring azure.blobPage/blobLister's own rationale exactly).
//
// Unlike azure.blobPage, this carries no continuation marker at all --
// see objectLister's own doc comment for why there's nothing here to
// expose: *storage.ObjectIterator.Next() already pages internally, so
// realObjectLister's own ListPage always returns every name there is in
// one call.
type objectPage struct {
	names []string
}

// objectLister is the minimal listing operation ListDependentApps
// needs, abstracted away from *storage.ObjectIterator (a generic
// struct, not an interface). Unlike aws.blobLister/azure's own
// equivalent, this has no explicit pagination step for
// ListDependentApps to drive: *storage.ObjectIterator.Next() already
// pages internally, so realObjectLister's own ListPage simply drains it
// completely in one call -- there is only ever one "page" from
// ListDependentApps' own point of view.
//
// Close releases the underlying *storage.Client's own resources (a
// gRPC/HTTP connection pool) -- the GCS client library's own docs
// recommend this once a client is no longer needed, unlike the AWS/
// Azure SDK clients aws.s3Lister/azure.blobLister wrap, neither of which
// exposes or requires an equivalent explicit close. Callers
// (cmd/cloudcompose's own checkNoDependentApps) must defer client.Close()
// after a successful NewObjectLister call.
type objectLister interface {
	ListPage(ctx context.Context, prefix string) (objectPage, error)
	Close() error
}

// realObjectLister adapts a real *storage.Client/BucketHandle to
// objectLister. Keeps the *storage.Client itself (not just the
// BucketHandle derived from it) so Close can release it -- see
// objectLister's own doc comment.
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

// NewObjectLister builds the real GCS listing client ListDependentApps
// needs from the ambient credential chain (Application Default
// Credentials -- environment variable, gcloud auth, or workload
// identity, whatever storage.NewClient already knows how to find; the
// same credential surface CI's own google-github-actions/auth and local
// `gcloud auth application-default login` already populate, mirroring
// aws.NewS3Client/azure.NewBlobContainerClient's own rationale for why
// this needs no new auth code of its own).
//
// Callers must defer client.Close() after a successful call -- see
// objectLister's own doc comment for why GCS's client specifically
// needs this where AWS's/Azure's don't.
func NewObjectLister(ctx context.Context, bucket string) (objectLister, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}
	return &realObjectLister{client: client, bucket: client.Bucket(bucket)}, nil
}

// ErrBackendListPermissionDenied mirrors
// aws.ErrBackendListPermissionDenied/azure.ErrBackendListPermissionDenied
// exactly -- see aws's own doc comment for the full rationale. GCS's own
// equivalent of S3's AccessDenied/Azure's 403 is an HTTP 403 response
// from the JSON API (confirmed against *googleapi.Error's own Code
// field).
var ErrBackendListPermissionDenied = errors.New("permission denied listing backend state objects")

// ListDependentApps lists every project name with its own state object
// under envName's own apps/ prefix in the bucket client points at,
// mirroring aws.ListDependentApps/azure.ListDependentApps' behavior and
// doc comments exactly -- see aws's own doc comment for the full
// rationale (sorted output, no need to open any app's state, etc).
//
// Unlike its AWS/Azure counterparts, this has no explicit pagination
// loop of its own: objectLister.ListPage already drains the entire
// listing (see its own doc comment for why), so ListDependentApps calls
// it exactly once.
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
