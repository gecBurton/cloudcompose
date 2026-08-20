package shared

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// TestAppBackendBlock_NilBackendReturnsNil confirms today's default (no
// backend: configured) produces no backend block at all, mirroring
// every environment generator's own nil-backend behavior (see
// docs/multi-user-state.md).
func TestAppBackendBlock_NilBackendReturnsNil(t *testing.T) {
	t.Parallel()
	if got := AppBackendBlock("prod", "checkout-api", nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// TestAppBackendBlock_AwsUsesAppSpecificKey confirms an app's own s3
// backend block uses BackendKeyForApp (never the environment's own
// BackendKeyForEnvironment key), and carries the same
// bucket/region/dynamodb_table facts the environment's own backend
// config declared.
func TestAppBackendBlock_AwsUsesAppSpecificKey(t *testing.T) {
	t.Parallel()
	backend := &models.BackendConfig{
		AWS: &models.AwsBackendConfig{
			Bucket:        "my-org-tfstate",
			Region:        "eu-west-2",
			DynamoDBTable: "my-org-tflocks",
		},
	}
	got := AppBackendBlock("prod", "checkout-api", backend)
	s3Backend, ok := got["s3"].(map[string]any)
	if !ok {
		t.Fatalf("expected s3 block, got %v", got)
	}
	if s3Backend["bucket"] != "my-org-tfstate" {
		t.Errorf("bucket = %v, want my-org-tfstate", s3Backend["bucket"])
	}
	if s3Backend["key"] != "cloudcompose/prod/apps/checkout-api.tfstate" {
		t.Errorf("key = %v, want cloudcompose/prod/apps/checkout-api.tfstate", s3Backend["key"])
	}
	if s3Backend["region"] != "eu-west-2" {
		t.Errorf("region = %v, want eu-west-2", s3Backend["region"])
	}
	if s3Backend["encrypt"] != true {
		t.Errorf("encrypt = %v, want true", s3Backend["encrypt"])
	}
	if s3Backend["dynamodb_table"] != "my-org-tflocks" {
		t.Errorf("dynamodb_table = %v, want my-org-tflocks", s3Backend["dynamodb_table"])
	}
}

// TestAppBackendBlock_AwsOmitsLockTableWhenNotConfigured confirms an
// absent lock table on the environment's own backend config produces no
// dynamodb_table key at all in the app's block either (not an empty
// string), mirroring aws.GenerateAwsEnvironment's own behavior.
func TestAppBackendBlock_AwsOmitsLockTableWhenNotConfigured(t *testing.T) {
	t.Parallel()
	backend := &models.BackendConfig{
		AWS: &models.AwsBackendConfig{Bucket: "my-org-tfstate", Region: "eu-west-2"},
	}
	got := AppBackendBlock("prod", "checkout-api", backend)
	s3Backend := got["s3"].(map[string]any)
	if _, ok := s3Backend["dynamodb_table"]; ok {
		t.Errorf("did not expect dynamodb_table key, got %v", s3Backend["dynamodb_table"])
	}
}

// TestAppBackendBlock_AzureUsesAppSpecificKey mirrors
// TestAppBackendBlock_AwsUsesAppSpecificKey for the azurerm backend.
func TestAppBackendBlock_AzureUsesAppSpecificKey(t *testing.T) {
	t.Parallel()
	backend := &models.BackendConfig{
		Azure: &models.AzureBackendConfig{
			ResourceGroupName:  "my-org-tfstate-rg",
			StorageAccountName: "myorgtfstate",
			ContainerName:      "tfstate",
		},
	}
	got := AppBackendBlock("prod", "checkout-api", backend)
	azurermBackend, ok := got["azurerm"].(map[string]any)
	if !ok {
		t.Fatalf("expected azurerm block, got %v", got)
	}
	if azurermBackend["resource_group_name"] != "my-org-tfstate-rg" {
		t.Errorf("resource_group_name = %v, want my-org-tfstate-rg", azurermBackend["resource_group_name"])
	}
	if azurermBackend["key"] != "cloudcompose/prod/apps/checkout-api.tfstate" {
		t.Errorf("key = %v, want cloudcompose/prod/apps/checkout-api.tfstate", azurermBackend["key"])
	}
	if azurermBackend["use_azuread_auth"] != true {
		t.Errorf("use_azuread_auth = %v, want true (the default)", azurermBackend["use_azuread_auth"])
	}
}

// TestAppBackendBlock_AzureRespectsExplicitUseAzureADAuthFalse confirms
// an explicit false on the environment's own backend config flows
// through to the app's block too, not just the default.
func TestAppBackendBlock_AzureRespectsExplicitUseAzureADAuthFalse(t *testing.T) {
	t.Parallel()
	useAzureADAuth := false
	backend := &models.BackendConfig{
		Azure: &models.AzureBackendConfig{
			ResourceGroupName:  "rg",
			StorageAccountName: "acct",
			ContainerName:      "tfstate",
			UseAzureADAuth:     &useAzureADAuth,
		},
	}
	got := AppBackendBlock("prod", "checkout-api", backend)
	azurermBackend := got["azurerm"].(map[string]any)
	if azurermBackend["use_azuread_auth"] != false {
		t.Errorf("use_azuread_auth = %v, want false", azurermBackend["use_azuread_auth"])
	}
}

// TestAppBackendBlock_GcpUsesAppSpecificPrefix mirrors
// TestAppBackendBlock_AwsUsesAppSpecificKey for the gcs backend, which
// uses "prefix" rather than "key".
func TestAppBackendBlock_GcpUsesAppSpecificPrefix(t *testing.T) {
	t.Parallel()
	backend := &models.BackendConfig{Gcp: &models.GcpBackendConfig{Bucket: "my-org-tfstate"}}
	got := AppBackendBlock("prod", "checkout-api", backend)
	gcsBackend, ok := got["gcs"].(map[string]any)
	if !ok {
		t.Fatalf("expected gcs block, got %v", got)
	}
	if gcsBackend["bucket"] != "my-org-tfstate" {
		t.Errorf("bucket = %v, want my-org-tfstate", gcsBackend["bucket"])
	}
	if gcsBackend["prefix"] != "cloudcompose/prod/apps/checkout-api.tfstate" {
		t.Errorf("prefix = %v, want cloudcompose/prod/apps/checkout-api.tfstate", gcsBackend["prefix"])
	}
}

// TestAppBackendBlock_DifferentAppsUnderSameEnvironmentGetDifferentKeys
// confirms two different projects sharing one environment's backend
// config get distinct keys -- the property that makes sharing one
// environment's backend across many apps safe at all.
func TestAppBackendBlock_DifferentAppsUnderSameEnvironmentGetDifferentKeys(t *testing.T) {
	t.Parallel()
	backend := &models.BackendConfig{
		AWS: &models.AwsBackendConfig{Bucket: "my-org-tfstate", Region: "eu-west-2"},
	}
	web := AppBackendBlock("prod", "web", backend)["s3"].(map[string]any)
	api := AppBackendBlock("prod", "checkout-api", backend)["s3"].(map[string]any)
	if web["key"] == api["key"] {
		t.Errorf("expected distinct keys, got both %v", web["key"])
	}
}
