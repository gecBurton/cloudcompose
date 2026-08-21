// Package aws contains AWS-specific inference and Terraform generation.
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
// letting tests substitute a fake without real AWS calls.
type s3Lister interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// NewS3Client builds a real S3 client from the ambient credential chain.
func NewS3Client(ctx context.Context, region string) (s3Lister, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS credentials: %w", err)
	}
	return s3.NewFromConfig(cfg), nil
}

// ErrBackendListPermissionDenied is returned by ListDependentApps when the
// list call fails with an AccessDenied error. Callers should treat this
// the same as "no backend configured" -- a warning, not a hard block on
// environment teardown.
var ErrBackendListPermissionDenied = errors.New("permission denied listing backend state objects")

// ListDependentApps lists every project with its own state object under
// envName's apps/ prefix in bucket, used by environment teardown's
// dependent-app safety check. Paginates automatically. Returns project
// names sorted for deterministic output.
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
