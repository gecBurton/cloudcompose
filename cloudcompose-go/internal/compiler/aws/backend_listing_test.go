package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
)

// fakeS3Client is a minimal in-memory stand-in for *s3.Client, mirroring
// status_test.go's own fakeECSClient/fakeELBClient rationale: lets
// tests assert ListDependentApps queried the right bucket/prefix
// without making a real AWS call. Supports pagination via a single
// split point (enough to exercise ListDependentApps' own
// continuation-token loop without needing a full paginator stand-in).
type fakeS3Client struct {
	keys []string
	// splitAfter, if > 0, returns keys[:splitAfter] with IsTruncated
	// true and a continuation token on the first call, then the rest on
	// the second call -- exercising ListDependentApps' pagination loop.
	splitAfter int
	// bucketSeen/prefixSeen record every value ListObjectsV2 was called
	// with, so tests can assert ListDependentApps pointed at the right
	// bucket/prefix.
	bucketSeen, prefixSeen []string
	// err, if set, is returned instead of a normal response.
	err error
}

func (f *fakeS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.bucketSeen = append(f.bucketSeen, aws.ToString(params.Bucket))
	f.prefixSeen = append(f.prefixSeen, aws.ToString(params.Prefix))
	if f.err != nil {
		return nil, f.err
	}

	keys := f.keys
	if f.splitAfter > 0 && f.splitAfter < len(f.keys) && params.ContinuationToken == nil {
		keys = f.keys[:f.splitAfter]
	} else if f.splitAfter > 0 && f.splitAfter < len(f.keys) {
		keys = f.keys[f.splitAfter:]
	}

	var contents []s3types.Object
	for _, k := range keys {
		key := k
		contents = append(contents, s3types.Object{Key: &key})
	}

	truncated := f.splitAfter > 0 && f.splitAfter < len(f.keys) && params.ContinuationToken == nil
	var nextToken *string
	if truncated {
		token := "next-page"
		nextToken = &token
	}
	return &s3.ListObjectsV2Output{
		Contents:              contents,
		IsTruncated:           aws.Bool(truncated),
		NextContinuationToken: nextToken,
	}, nil
}

// TestListDependentApps_QueriesBucketAndPrefix confirms
// ListDependentApps calls ListObjectsV2 with exactly the bucket/prefix
// derived from its own arguments, not something re-derived a different
// way.
func TestListDependentApps_QueriesBucketAndPrefix(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{}
	_, err := ListDependentApps(context.Background(), client, "my-org-tfstate", "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	if len(client.bucketSeen) != 1 || client.bucketSeen[0] != "my-org-tfstate" {
		t.Errorf("bucketSeen = %v, want [my-org-tfstate]", client.bucketSeen)
	}
	if len(client.prefixSeen) != 1 || client.prefixSeen[0] != "cloudcompose/prod/apps/" {
		t.Errorf("prefixSeen = %v, want [cloudcompose/prod/apps/]", client.prefixSeen)
	}
}

// TestListDependentApps_RecoversProjectNamesFromKeys confirms every
// listed object key is turned back into its project name (not returned
// as a raw key), and that keys are returned sorted for deterministic
// output.
func TestListDependentApps_RecoversProjectNamesFromKeys(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{
		keys: []string{
			"cloudcompose/prod/apps/web.tfstate",
			"cloudcompose/prod/apps/checkout-api.tfstate",
		},
	}
	got, err := ListDependentApps(context.Background(), client, "my-org-tfstate", "prod")
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

// TestListDependentApps_NoAppsReturnsEmpty confirms an environment with
// no dependent apps at all (an empty listing) returns an empty slice,
// not an error -- the common case every environment starts in.
func TestListDependentApps_NoAppsReturnsEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{}
	got, err := ListDependentApps(context.Background(), client, "my-org-tfstate", "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// TestListDependentApps_IgnoresKeysNotMatchingPrefixConvention confirms
// a key that happens to be listed under the prefix but doesn't parse as
// a well-formed app key (shared.ProjectNameFromAppKey returns false) is
// skipped rather than surfaced as a garbage project name -- defensive
// against anything else that might end up in the same bucket.
func TestListDependentApps_IgnoresKeysNotMatchingPrefixConvention(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{
		keys: []string{
			"cloudcompose/prod/apps/web.tfstate",
			"cloudcompose/prod/apps/not-a-state-file.txt",
		},
	}
	got, err := ListDependentApps(context.Background(), client, "my-org-tfstate", "prod")
	if err != nil {
		t.Fatalf("ListDependentApps failed: %v", err)
	}
	if len(got) != 1 || got[0] != "web" {
		t.Errorf("got %v, want [web]", got)
	}
}

// TestListDependentApps_PaginatesAcrossMultiplePages confirms
// ListDependentApps follows IsTruncated/NextContinuationToken to
// collect every app across more than one ListObjectsV2 call, not just
// the first page -- real environments with many apps will need this.
func TestListDependentApps_PaginatesAcrossMultiplePages(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{
		keys: []string{
			"cloudcompose/prod/apps/web.tfstate",
			"cloudcompose/prod/apps/checkout-api.tfstate",
			"cloudcompose/prod/apps/billing.tfstate",
		},
		splitAfter: 1,
	}
	got, err := ListDependentApps(context.Background(), client, "my-org-tfstate", "prod")
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
	if len(client.bucketSeen) != 2 {
		t.Errorf("expected 2 ListObjectsV2 calls (one per page), got %d", len(client.bucketSeen))
	}
}

// TestListDependentApps_AccessDeniedReturnsSentinelError confirms a
// permissions failure surfaces as ErrBackendListPermissionDenied
// specifically (via errors.Is), so callers (environment teardown) can
// distinguish it from every other failure and degrade to a warning
// instead of a hard error -- see docs/multi-user-state.md's "IAM
// footprint" note.
func TestListDependentApps_AccessDeniedReturnsSentinelError(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{
		err: &smithy.GenericAPIError{Code: "AccessDenied", Message: "Access Denied"},
	}
	_, err := ListDependentApps(context.Background(), client, "my-org-tfstate", "prod")
	if !errors.Is(err, ErrBackendListPermissionDenied) {
		t.Errorf("expected ErrBackendListPermissionDenied, got %v", err)
	}
}

// TestListDependentApps_OtherErrorsDoNotMatchSentinel confirms a
// genuinely different failure (here: a different error code entirely)
// is not mistaken for the specific AccessDenied case -- only that one
// error code degrades to the permission-denied sentinel.
func TestListDependentApps_OtherErrorsDoNotMatchSentinel(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{
		err: &smithy.GenericAPIError{Code: "NoSuchBucket", Message: "The specified bucket does not exist"},
	}
	_, err := ListDependentApps(context.Background(), client, "my-org-tfstate", "prod")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrBackendListPermissionDenied) {
		t.Errorf("did not expect ErrBackendListPermissionDenied for a NoSuchBucket error")
	}
}
