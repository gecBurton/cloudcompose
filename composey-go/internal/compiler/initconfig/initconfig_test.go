package initconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gecburton/composey/internal/models"
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
