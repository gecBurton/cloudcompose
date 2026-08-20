package azure

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// fakeBlobLister is a minimal in-memory stand-in for blobLister,
// mirroring aws/backend_listing_test.go's own fakeS3Client rationale:
// lets tests assert ListDependentApps queried the right prefix without
// a real Azure call. Supports pagination via a single split point.
type fakeBlobLister struct {
	names []string
	// splitAfter, if > 0, returns names[:splitAfter] with a non-nil
	// marker on the first call, then the rest on the second call --
	// exercising ListDependentApps' pagination loop.
	splitAfter int
	// prefixSeen records every prefix ListPage was called with.
	prefixSeen []string
	// err, if set, is returned instead of a normal response.
	err error
}

func (f *fakeBlobLister) ListPage(ctx context.Context, prefix string, marker *string) (blobPage, error) {
	f.prefixSeen = append(f.prefixSeen, prefix)
	if f.err != nil {
		return blobPage{}, f.err
	}

	names := f.names
	if f.splitAfter > 0 && f.splitAfter < len(f.names) && marker == nil {
		names = f.names[:f.splitAfter]
	} else if f.splitAfter > 0 && f.splitAfter < len(f.names) {
		names = f.names[f.splitAfter:]
	}

	var nextMarker *string
	if f.splitAfter > 0 && f.splitAfter < len(f.names) && marker == nil {
		token := "next-page"
		nextMarker = &token
	}
	return blobPage{names: names, nextMarker: nextMarker}, nil
}

// TestListDependentApps_QueriesPrefix confirms ListDependentApps calls
// ListPage with exactly the prefix derived from its own envName
// argument, not something re-derived a different way.
func TestListDependentApps_QueriesPrefix(t *testing.T) {
	t.Parallel()
	client := &fakeBlobLister{}
	_, err := ListDependentApps(context.Background(), client, "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	if len(client.prefixSeen) != 1 || client.prefixSeen[0] != "cloudcompose/prod/apps/" {
		t.Errorf("prefixSeen = %v, want [cloudcompose/prod/apps/]", client.prefixSeen)
	}
}

// TestListDependentApps_RecoversProjectNamesFromBlobNames mirrors
// aws.TestListDependentApps_RecoversProjectNamesFromKeys.
func TestListDependentApps_RecoversProjectNamesFromBlobNames(t *testing.T) {
	t.Parallel()
	client := &fakeBlobLister{
		names: []string{
			"cloudcompose/prod/apps/web.tfstate",
			"cloudcompose/prod/apps/checkout-api.tfstate",
		},
	}
	got, err := ListDependentApps(context.Background(), client, "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	want := []string{"checkout-api", "web"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
}

// TestListDependentApps_NoAppsReturnsEmpty mirrors
// aws.TestListDependentApps_NoAppsReturnsEmpty.
func TestListDependentApps_NoAppsReturnsEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeBlobLister{}
	got, err := ListDependentApps(context.Background(), client, "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// TestListDependentApps_IgnoresBlobsNotMatchingPrefixConvention mirrors
// aws.TestListDependentApps_IgnoresKeysNotMatchingPrefixConvention.
func TestListDependentApps_IgnoresBlobsNotMatchingPrefixConvention(t *testing.T) {
	t.Parallel()
	client := &fakeBlobLister{
		names: []string{
			"cloudcompose/prod/apps/web.tfstate",
			"cloudcompose/prod/apps/not-a-state-file.txt",
		},
	}
	got, err := ListDependentApps(context.Background(), client, "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	if len(got) != 1 || got[0] != "web" {
		t.Errorf("got %v, want [web]", got)
	}
}

// TestListDependentApps_PaginatesAcrossMultiplePages mirrors
// aws.TestListDependentApps_PaginatesAcrossMultiplePages.
func TestListDependentApps_PaginatesAcrossMultiplePages(t *testing.T) {
	t.Parallel()
	client := &fakeBlobLister{
		names: []string{
			"cloudcompose/prod/apps/web.tfstate",
			"cloudcompose/prod/apps/checkout-api.tfstate",
			"cloudcompose/prod/apps/billing.tfstate",
		},
		splitAfter: 1,
	}
	got, err := ListDependentApps(context.Background(), client, "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	want := []string{"billing", "checkout-api", "web"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
	if len(client.prefixSeen) != 2 {
		t.Errorf("expected 2 ListPage calls (one per page), got %d", len(client.prefixSeen))
	}
}

// TestListDependentApps_ForbiddenReturnsSentinelError confirms a 403
// response surfaces as ErrBackendListPermissionDenied specifically (via
// errors.Is), mirroring
// aws.TestListDependentApps_AccessDeniedReturnsSentinelError -- Azure's
// own equivalent of S3's AccessDenied error code.
func TestListDependentApps_ForbiddenReturnsSentinelError(t *testing.T) {
	t.Parallel()
	client := &fakeBlobLister{
		err: &azcore.ResponseError{StatusCode: http.StatusForbidden},
	}
	_, err := ListDependentApps(context.Background(), client, "prod")
	if !errors.Is(err, ErrBackendListPermissionDenied) {
		t.Errorf("expected ErrBackendListPermissionDenied, got %v", err)
	}
}

// TestListDependentApps_OtherErrorsDoNotMatchSentinel mirrors
// aws.TestListDependentApps_OtherErrorsDoNotMatchSentinel.
func TestListDependentApps_OtherErrorsDoNotMatchSentinel(t *testing.T) {
	t.Parallel()
	client := &fakeBlobLister{
		err: &azcore.ResponseError{StatusCode: http.StatusNotFound},
	}
	_, err := ListDependentApps(context.Background(), client, "prod")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrBackendListPermissionDenied) {
		t.Errorf("did not expect ErrBackendListPermissionDenied for a 404 response")
	}
}
