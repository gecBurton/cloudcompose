package initconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

func TestLoad_ReturnsNilWhenFileDoesNotExist(t *testing.T) {
	t.Parallel()
	config, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if config != nil {
		t.Errorf("expected nil config, got %+v", config)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "environment.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestLoad_ValidAwsConfig(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: aws
name: prod
region: eu-west-2
retain_data_on_destroy: true
tags:
  Team: platform
aws:
  vpc_cidr: 10.0.0.0/16
  az_count: 2
  create_alb: true
`)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if config.Provider != "aws" || config.Name != "prod" || config.Region != "eu-west-2" {
		t.Errorf("unexpected config: %+v", config)
	}
	if config.AWS == nil || config.AWS.VpcCIDR != "10.0.0.0/16" {
		t.Fatalf("expected aws block with vpc_cidr, got %+v", config.AWS)
	}
	if config.AWS.AzCount == nil || *config.AWS.AzCount != 2 {
		t.Errorf("az_count = %v, want 2", config.AWS.AzCount)
	}
	if config.Azure != nil || config.Gcp != nil {
		t.Errorf("expected only aws block populated, got azure=%+v gcp=%+v", config.Azure, config.Gcp)
	}
}

func TestLoad_RejectsUnknownTopLevelKey(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: aws
name: prod
bogus_field: oops
`)
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected an error for an unknown top-level key")
	}
}

func TestLoad_RejectsMismatchedProviderBlock(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: aws
name: prod
aws:
  vpc_cidr: 10.0.0.0/16
azure:
  vnet_cidr: 10.0.0.0/16
`)
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected an error when both aws and azure blocks are present")
	}
}

func TestLoad_RejectsUnsupportedProvider(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: openstack
name: prod
`)
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected an error for an unsupported provider")
	}
}

func TestLoad_RejectsMissingName(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: aws
`)
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected an error when name is missing")
	}
}

// TestLoad_RejectsNameContainingSlash is the regression test for the
// backend-state-key collision shared.ValidateBackendName exists to
// prevent (see its own doc comment): an environment name is also the
// input to shared.BackendKeyForEnvironment, so it must be rejected here
// before it can ever reach that function.
func TestLoad_RejectsNameContainingSlash(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: aws
name: prod/apps
aws:
  vpc_cidr: 10.0.0.0/16
`)
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected an error when name contains '/'")
	}
}

func TestLoad_GcpRequiresProjectID(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: gcp
name: prod
gcp:
  vpc_cidr: 10.0.0.0/16
`)
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected an error when gcp.project_id is missing")
	}
}

func TestLoad_GcpWithProjectIDSucceeds(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: gcp
name: prod
gcp:
  vpc_cidr: 10.0.0.0/16
  project_id: my-project
`)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if config.Gcp == nil || config.Gcp.ProjectID != "my-project" {
		t.Fatalf("expected gcp.project_id = my-project, got %+v", config.Gcp)
	}
}

func TestLoad_TopLevelDomain(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: gcp
name: prod
domain: example.com
gcp:
  vpc_cidr: 10.0.0.0/16
  project_id: my-project
`)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if config.Domain == nil || *config.Domain != "example.com" {
		t.Fatalf("expected domain = example.com, got %+v", config.Domain)
	}
}

func TestLoad_AzureConfig(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: azure
name: prod
azure:
  vnet_cidr: 10.0.0.0/16
`)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if config.Azure == nil || config.Azure.VnetCIDR != "10.0.0.0/16" {
		t.Fatalf("expected azure.vnet_cidr = 10.0.0.0/16, got %+v", config.Azure)
	}
}

func TestValidate_RejectsBlockNotMatchingProvider(t *testing.T) {
	t.Parallel()
	config := &models.InitConfig{
		Provider: "aws",
		Name:     "prod",
		AWS:      &models.AwsInitConfig{VpcCIDR: "10.0.0.0/16"},
		Gcp:      &models.GcpInitConfig{ProjectID: "leftover-from-a-copy-paste"},
	}
	if err := Validate(config); err == nil {
		t.Fatalf("expected an error for a gcp block present alongside provider: aws")
	}
}

func TestLoad_BackendAwsConfig(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: aws
name: prod
aws:
  vpc_cidr: 10.0.0.0/16
backend:
  aws:
    bucket: my-org-tfstate
    region: eu-west-2
    dynamodb_table: my-org-tflocks
`)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if config.Backend == nil || config.Backend.AWS == nil {
		t.Fatalf("expected backend.aws block, got %+v", config.Backend)
	}
	if config.Backend.AWS.Bucket != "my-org-tfstate" || config.Backend.AWS.Region != "eu-west-2" {
		t.Errorf("unexpected backend.aws: %+v", config.Backend.AWS)
	}
	if config.Backend.AWS.DynamoDBTable != "my-org-tflocks" {
		t.Errorf("dynamodb_table = %q, want my-org-tflocks", config.Backend.AWS.DynamoDBTable)
	}
}

func TestLoad_BackendOmittedIsValid(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: aws
name: prod
aws:
  vpc_cidr: 10.0.0.0/16
`)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if config.Backend != nil {
		t.Errorf("expected nil backend, got %+v", config.Backend)
	}
}

func TestLoad_RejectsBackendMissingRequiredAwsFields(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `
provider: aws
name: prod
aws:
  vpc_cidr: 10.0.0.0/16
backend:
  aws:
    bucket: my-org-tfstate
`)
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected an error when backend.aws is missing region")
	}
}

func TestValidate_RejectsBackendBlockNotMatchingProvider(t *testing.T) {
	t.Parallel()
	config := &models.InitConfig{
		Provider: "aws",
		Name:     "prod",
		AWS:      &models.AwsInitConfig{VpcCIDR: "10.0.0.0/16"},
		Backend: &models.BackendConfig{
			Azure: &models.AzureBackendConfig{
				ResourceGroupName:  "rg",
				StorageAccountName: "acct",
				ContainerName:      "tfstate",
			},
		},
	}
	if err := Validate(config); err == nil {
		t.Fatalf("expected an error for a backend.azure block present alongside provider: aws")
	}
}

func TestValidate_RejectsBackendAzureMissingRequiredFields(t *testing.T) {
	t.Parallel()
	config := &models.InitConfig{
		Provider: "azure",
		Name:     "prod",
		Azure:    &models.AzureInitConfig{VnetCIDR: "10.0.0.0/16"},
		Backend: &models.BackendConfig{
			Azure: &models.AzureBackendConfig{ResourceGroupName: "rg"},
		},
	}
	if err := Validate(config); err == nil {
		t.Fatalf("expected an error when backend.azure is missing storage_account_name/container_name")
	}
}

func TestValidate_RejectsBackendGcpMissingBucket(t *testing.T) {
	t.Parallel()
	config := &models.InitConfig{
		Provider: "gcp",
		Name:     "prod",
		Gcp:      &models.GcpInitConfig{ProjectID: "my-project"},
		Backend:  &models.BackendConfig{Gcp: &models.GcpBackendConfig{}},
	}
	if err := Validate(config); err == nil {
		t.Fatalf("expected an error when backend.gcp is missing bucket")
	}
}

func TestBackendWarnings_NoBackendConfigured(t *testing.T) {
	t.Parallel()
	config := &models.InitConfig{Provider: "aws", Name: "prod"}
	warnings := BackendWarnings(config)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning, got %v", warnings)
	}
}

func TestBackendWarnings_AwsBackendWithoutLockTable(t *testing.T) {
	t.Parallel()
	config := &models.InitConfig{
		Provider: "aws",
		Name:     "prod",
		Backend: &models.BackendConfig{
			AWS: &models.AwsBackendConfig{Bucket: "my-org-tfstate", Region: "eu-west-2"},
		},
	}
	warnings := BackendWarnings(config)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning about the missing dynamodb_table, got %v", warnings)
	}
}

func TestBackendWarnings_AwsBackendWithLockTableHasNoWarnings(t *testing.T) {
	t.Parallel()
	config := &models.InitConfig{
		Provider: "aws",
		Name:     "prod",
		Backend: &models.BackendConfig{
			AWS: &models.AwsBackendConfig{Bucket: "my-org-tfstate", Region: "eu-west-2", DynamoDBTable: "my-org-tflocks"},
		},
	}
	if warnings := BackendWarnings(config); len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestBackendWarnings_AzureAndGcpHaveNoLockTableWarning(t *testing.T) {
	t.Parallel()
	config := &models.InitConfig{
		Provider: "azure",
		Name:     "prod",
		Backend: &models.BackendConfig{
			Azure: &models.AzureBackendConfig{
				ResourceGroupName:  "rg",
				StorageAccountName: "acct",
				ContainerName:      "tfstate",
			},
		},
	}
	if warnings := BackendWarnings(config); len(warnings) != 0 {
		t.Errorf("expected no warnings for a configured azure backend, got %v", warnings)
	}
}
