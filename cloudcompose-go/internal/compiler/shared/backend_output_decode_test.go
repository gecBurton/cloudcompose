package shared

import (
	"testing"
)

// TestDecodeBackendOutput_Nil confirms a nil raw map (an environment
// with no `backend` output at all -- see
// OptionalTerraformOutputs's own doc comment) decodes to a nil
// *models.BackendConfig, not an empty-but-non-nil one.
func TestDecodeBackendOutput_Nil(t *testing.T) {
	t.Parallel()
	if got := DecodeBackendOutput(nil); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestDecodeBackendOutput_Aws confirms a well-formed AWS shape decodes
// every field, including the optional dynamodb_table.
func TestDecodeBackendOutput_Aws(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"provider": "aws",
		"aws": map[string]any{
			"bucket":         "my-org-tfstate",
			"region":         "eu-west-2",
			"dynamodb_table": "my-org-tflocks",
		},
	}
	got := DecodeBackendOutput(raw)
	if got == nil || got.AWS == nil {
		t.Fatalf("expected AWS backend, got %+v", got)
	}
	if got.AWS.Bucket != "my-org-tfstate" || got.AWS.Region != "eu-west-2" || got.AWS.DynamoDBTable != "my-org-tflocks" {
		t.Errorf("unexpected AWS backend: %+v", got.AWS)
	}
}

// TestDecodeBackendOutput_AwsWithoutLockTable confirms an absent
// dynamodb_table field decodes to an empty string, not a decode
// failure -- mirroring aws.GenerateAwsEnvironment's own choice to omit
// the field entirely rather than write an empty string when no lock
// table is configured.
func TestDecodeBackendOutput_AwsWithoutLockTable(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"provider": "aws",
		"aws": map[string]any{
			"bucket": "my-org-tfstate",
			"region": "eu-west-2",
		},
	}
	got := DecodeBackendOutput(raw)
	if got == nil || got.AWS == nil {
		t.Fatalf("expected AWS backend, got %+v", got)
	}
	if got.AWS.DynamoDBTable != "" {
		t.Errorf("DynamoDBTable = %q, want empty", got.AWS.DynamoDBTable)
	}
}

// TestDecodeBackendOutput_Azure confirms a well-formed Azure shape
// decodes every field, including use_azuread_auth as a real *bool
// (distinguishing "false" from "not present" -- see
// AzureBackendConfig's own doc comment in internal/models/init_config.go).
func TestDecodeBackendOutput_Azure(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"provider": "azure",
		"azure": map[string]any{
			"resource_group_name":  "my-org-tfstate-rg",
			"storage_account_name": "myorgtfstate",
			"container_name":       "tfstate",
			"use_azuread_auth":     false,
		},
	}
	got := DecodeBackendOutput(raw)
	if got == nil || got.Azure == nil {
		t.Fatalf("expected Azure backend, got %+v", got)
	}
	if got.Azure.ResourceGroupName != "my-org-tfstate-rg" ||
		got.Azure.StorageAccountName != "myorgtfstate" ||
		got.Azure.ContainerName != "tfstate" {
		t.Errorf("unexpected Azure backend: %+v", got.Azure)
	}
	if got.Azure.UseAzureADAuth == nil || *got.Azure.UseAzureADAuth != false {
		t.Errorf("UseAzureADAuth = %v, want a pointer to false", got.Azure.UseAzureADAuth)
	}
}

// TestDecodeBackendOutput_AzureUseAzureADAuthAbsentIsNilNotFalse
// confirms an absent use_azuread_auth field decodes to a nil pointer
// (meaning "not set", letting a caller apply its own default), not a
// non-nil pointer to false -- the exact distinction *bool exists to
// make representable in the first place.
func TestDecodeBackendOutput_AzureUseAzureADAuthAbsentIsNilNotFalse(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"provider": "azure",
		"azure": map[string]any{
			"resource_group_name":  "rg",
			"storage_account_name": "acct",
			"container_name":       "tfstate",
		},
	}
	got := DecodeBackendOutput(raw)
	if got == nil || got.Azure == nil {
		t.Fatalf("expected Azure backend, got %+v", got)
	}
	if got.Azure.UseAzureADAuth != nil {
		t.Errorf("UseAzureADAuth = %v, want nil (not set)", got.Azure.UseAzureADAuth)
	}
}

// TestDecodeBackendOutput_AzureUseAzureADAuthWrongTypeIsTreatedAsAbsent
// confirms a malformed (non-bool) use_azuread_auth value degrades to
// "not set" rather than panicking or silently coercing -- defensive
// against a hand-edited or otherwise malformed Terraform state this
// function has no control over the shape of.
func TestDecodeBackendOutput_AzureUseAzureADAuthWrongTypeIsTreatedAsAbsent(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"provider": "azure",
		"azure": map[string]any{
			"resource_group_name":  "rg",
			"storage_account_name": "acct",
			"container_name":       "tfstate",
			"use_azuread_auth":     "true", // string, not bool
		},
	}
	got := DecodeBackendOutput(raw)
	if got == nil || got.Azure == nil {
		t.Fatalf("expected Azure backend, got %+v", got)
	}
	if got.Azure.UseAzureADAuth != nil {
		t.Errorf("UseAzureADAuth = %v, want nil for a malformed value", got.Azure.UseAzureADAuth)
	}
}

// TestDecodeBackendOutput_Gcp confirms a well-formed GCP shape decodes.
func TestDecodeBackendOutput_Gcp(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"provider": "gcp",
		"gcp":      map[string]any{"bucket": "my-org-tfstate"},
	}
	got := DecodeBackendOutput(raw)
	if got == nil || got.Gcp == nil {
		t.Fatalf("expected GCP backend, got %+v", got)
	}
	if got.Gcp.Bucket != "my-org-tfstate" {
		t.Errorf("Bucket = %q, want my-org-tfstate", got.Gcp.Bucket)
	}
}

// TestDecodeBackendOutput_UnrecognizedProviderReturnsNil confirms a
// provider value that isn't one of aws/azure/gcp decodes to nil rather
// than a partially-populated or guessed backend -- defensive against a
// state file this function has no control over the shape of (e.g. an
// old cloudcompose version writing a different shape in the future, or
// hand-edited state).
func TestDecodeBackendOutput_UnrecognizedProviderReturnsNil(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"provider": "digitalocean",
		"aws":      map[string]any{"bucket": "b", "region": "r"},
	}
	if got := DecodeBackendOutput(raw); got != nil {
		t.Errorf("expected nil for an unrecognized provider, got %+v", got)
	}
}

// TestDecodeBackendOutput_ProviderPresentButOwnBlockMissingReturnsNil
// confirms a `provider: "aws"` with no matching "aws" block at all
// (rather than a malformed one) also decodes to nil, not a backend with
// every field zero-valued -- the same "nothing usable here" outcome as
// no backend output at all, rather than a backend that looks configured
// but silently isn't.
func TestDecodeBackendOutput_ProviderPresentButOwnBlockMissingReturnsNil(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"provider": "aws"}
	if got := DecodeBackendOutput(raw); got != nil {
		t.Errorf("expected nil when the aws block itself is missing, got %+v", got)
	}
}

// TestDecodeBackendOutput_MalformedBlockShapeReturnsNil confirms a
// "aws" key present but not itself an object (map[string]any) also
// decodes to nil rather than panicking on a failed type assertion.
func TestDecodeBackendOutput_MalformedBlockShapeReturnsNil(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"provider": "aws", "aws": "not-an-object"}
	if got := DecodeBackendOutput(raw); got != nil {
		t.Errorf("expected nil for a malformed aws block, got %+v", got)
	}
}

// TestDecodeBackendOutput_MissingProviderFieldReturnsNil confirms a raw
// map with no "provider" key at all (as opposed to an empty or
// unrecognized one) also decodes to nil via the same default case.
func TestDecodeBackendOutput_MissingProviderFieldReturnsNil(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"aws": map[string]any{"bucket": "b", "region": "r"}}
	if got := DecodeBackendOutput(raw); got != nil {
		t.Errorf("expected nil when provider is missing entirely, got %+v", got)
	}
}
