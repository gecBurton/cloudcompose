package gcp

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/api/googleapi"
)

// fakeObjectLister is a minimal in-memory stand-in for objectLister,
// mirroring aws/backend_listing_test.go's own fakeS3Client and
// azure/backend_listing_test.go's own fakeBlobLister rationale: lets
// tests assert ListDependentApps queried the right prefix without a
// real GCS call.
type fakeObjectLister struct {
	names []string
	// prefixSeen records every prefix ListPage was called with.
	prefixSeen []string
	// err, if set, is returned instead of a normal response.
	err error
}

func (f *fakeObjectLister) ListPage(ctx context.Context, prefix string) (objectPage, error) {
	f.prefixSeen = append(f.prefixSeen, prefix)
	if f.err != nil {
		return objectPage{}, f.err
	}
	return objectPage{names: f.names}, nil
}

// Close is a no-op here -- ListDependentApps itself never calls Close
// (it doesn't own the client's lifecycle; see objectLister's own doc
// comment: the caller that constructs a client via NewObjectLister is
// responsible for closing it, not ListDependentApps), so nothing in
// this test file needs to observe whether it was called.
func (f *fakeObjectLister) Close() error {
	return nil
}

// TestListDependentApps_QueriesPrefix confirms ListDependentApps calls
// ListPage with exactly the prefix derived from its own envName
// argument, not something re-derived a different way.
func TestListDependentApps_QueriesPrefix(t *testing.T) {
	t.Parallel()
	client := &fakeObjectLister{}
	_, err := ListDependentApps(context.Background(), client, "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	if len(client.prefixSeen) != 1 || client.prefixSeen[0] != "cloudcompose/prod/apps/" {
		t.Errorf("prefixSeen = %v, want [cloudcompose/prod/apps/]", client.prefixSeen)
	}
}

// TestListDependentApps_RecoversProjectNamesFromObjectNames mirrors
// aws.TestListDependentApps_RecoversProjectNamesFromKeys/
// azure.TestListDependentApps_RecoversProjectNamesFromBlobNames.
func TestListDependentApps_RecoversProjectNamesFromObjectNames(t *testing.T) {
	t.Parallel()
	client := &fakeObjectLister{
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
	client := &fakeObjectLister{}
	got, err := ListDependentApps(context.Background(), client, "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// TestListDependentApps_IgnoresObjectsNotMatchingPrefixConvention
// mirrors
// aws.TestListDependentApps_IgnoresKeysNotMatchingPrefixConvention.
func TestListDependentApps_IgnoresObjectsNotMatchingPrefixConvention(t *testing.T) {
	t.Parallel()
	client := &fakeObjectLister{
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

// TestListDependentApps_ForbiddenReturnsSentinelError confirms a 403
// response surfaces as ErrBackendListPermissionDenied specifically (via
// errors.Is), mirroring
// aws.TestListDependentApps_AccessDeniedReturnsSentinelError/
// azure.TestListDependentApps_ForbiddenReturnsSentinelError.
func TestListDependentApps_ForbiddenReturnsSentinelError(t *testing.T) {
	t.Parallel()
	client := &fakeObjectLister{
		err: &googleapi.Error{Code: 403, Message: "Permission denied"},
	}
	_, err := ListDependentApps(context.Background(), client, "prod")
	if !errors.Is(err, ErrBackendListPermissionDenied) {
		t.Errorf("expected ErrBackendListPermissionDenied, got %v", err)
	}
}

// TestListDependentApps_OtherErrorsDoNotMatchSentinel mirrors
// aws.TestListDependentApps_OtherErrorsDoNotMatchSentinel/
// azure.TestListDependentApps_OtherErrorsDoNotMatchSentinel.
func TestListDependentApps_OtherErrorsDoNotMatchSentinel(t *testing.T) {
	t.Parallel()
	client := &fakeObjectLister{
		err: &googleapi.Error{Code: 404, Message: "Not found"},
	}
	_, err := ListDependentApps(context.Background(), client, "prod")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrBackendListPermissionDenied) {
		t.Errorf("did not expect ErrBackendListPermissionDenied for a 404 response")
	}
}
