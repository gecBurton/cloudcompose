// Package aws contains AWS-specific inference and Terraform generation.
// This file adds a separate concern: listing every app's own state
// object under an environment's own backend-key prefix, the mechanism
// environment teardown's dependent-app safety check depends on to
// refuse (by default) tearing down an environment other apps still
// depend on. See docs/multi-user-state.md's "Safe environment
// teardown" section.
package aws

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
)

// s3Lister is the subset of *s3.Client that ListDependentApps needs,
// mirroring status.go's own ecsClient/elbClient rationale: lets tests
// substitute a fake without real AWS calls or credentials.
type s3Lister interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// NewS3Client builds the real S3 client ListDependentApps needs from the
// ambient credential chain, mirroring NewAWSClients' own rationale
// (status.go) exactly.
func NewS3Client(ctx context.Context, region string) (s3Lister, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS credentials: %w", err)
	}
	return s3.NewFromConfig(cfg), nil
}

// ErrBackendListPermissionDenied is returned by ListDependentApps when
// the list call itself fails on what looks like a permissions problem
// (AccessDenied -- confirmed against the real S3 API's own error code,
// not guessed from an HTTP status alone), as opposed to any other
// failure. Callers should treat this the same way they treat "no
// backend configured at all" -- a warning, not a hard block on
// environment teardown -- since docs/multi-user-state.md's own "IAM
// footprint" section notes s3:ListBucket is a broader permission than a
// backend strictly needs, and a locked-down org may reasonably not
// grant it.
var ErrBackendListPermissionDenied = errors.New("permission denied listing backend state objects")

// ListDependentApps lists every project name with its own state object
// under envName's own apps/ prefix in bucket (see
// shared.BackendAppsPrefix), the check environment teardown's
// dependent-app safety relies on. Returns project names only (recovered
// from each object's own key via shared.ProjectNameFromAppKey -- no
// need to open any app's state to find out what it's called), sorted
// for deterministic output.
//
// Paginates automatically: ListObjectsV2 returns at most 1000 keys per
// call, so environments with many apps need more than one round trip.
func ListDependentApps(ctx context.Context, client s3Lister, bucket, envName string) ([]string, error) {
	prefix := shared.BackendAppsPrefix(envName)

	var projectNames []string
	var continuationToken *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "AccessDenied" {
				return nil, fmt.Errorf("%w: %s", ErrBackendListPermissionDenied, err)
			}
			return nil, fmt.Errorf("list objects under %s in bucket %s: %w", prefix, bucket, err)
		}

		for _, obj := range out.Contents {
			if obj.Key == nil {
				continue
			}
			if projectName, ok := shared.ProjectNameFromAppKey(envName, *obj.Key); ok {
				projectNames = append(projectNames, projectName)
			}
		}

		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		continuationToken = out.NextContinuationToken
	}

	sort.Strings(projectNames)
	return projectNames, nil
}
