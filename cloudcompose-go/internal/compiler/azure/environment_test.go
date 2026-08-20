package azure

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeEnvironmentOutputsFixture mirrors
// aws/environment_test.go's own helper of the same name -- see its own
// doc comment for why each package keeps a local copy.
func writeEnvironmentOutputsFixture(t *testing.T, environmentValueHCL, backendValueHCL string) string {
	t.Helper()
	dir := t.TempDir()

	mainTF := fmt.Sprintf(`output "environment" {
  value = %s
}
`, environmentValueHCL)
	if backendValueHCL != "" {
		mainTF += fmt.Sprintf(`
output "backend" {
  value = %s
}
`, backendValueHCL)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(mainTF), 0644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	initCmd := exec.Command("terraform", "init", "-input=false")
	initCmd.Dir = dir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("terraform init: %v\n%s", err, out)
	}

	applyCmd := exec.Command("terraform", "apply", "-auto-approve")
	applyCmd.Dir = dir
	if out, err := applyCmd.CombinedOutput(); err != nil {
		t.Fatalf("terraform apply: %v\n%s", err, out)
	}

	return dir
}

const minimalAzureEnvironmentHCL = `{
    target                      = "azure"
    name                        = "prod"
    log_analytics_workspace_id  = "x"
    resource_group_name         = "prod-rg"
    vnet_id                     = "y"
    vnet_name                   = "prod-vnet"
    apps_cidr                   = "10.0.128.0/17"
  }`

// TestLoadAzureEnvironment_NoBackendOutputLeavesBackendNil mirrors
// aws.TestLoadAwsEnvironment_NoBackendOutputLeavesBackendNil.
func TestLoadAzureEnvironment_NoBackendOutputLeavesBackendNil(t *testing.T) {
	t.Parallel()
	dir := writeEnvironmentOutputsFixture(t, minimalAzureEnvironmentHCL, "")

	env, err := LoadAzureEnvironment(dir)
	if err != nil {
		t.Fatalf("LoadAzureEnvironment failed: %v", err)
	}
	if env.Backend != nil {
		t.Errorf("expected nil Backend, got %+v", env.Backend)
	}
}

// TestLoadAzureEnvironment_DecodesBackendOutput mirrors
// aws.TestLoadAwsEnvironment_DecodesBackendOutput: a real,
// end-to-end roundtrip of azure.GenerateAzureEnvironment's own
// `output "backend"` shape back into env.Backend.Azure.
func TestLoadAzureEnvironment_DecodesBackendOutput(t *testing.T) {
	t.Parallel()
	backendValueHCL := `{
    provider = "azure"
    azure = {
      resource_group_name  = "my-org-tfstate-rg"
      storage_account_name = "myorgtfstate"
      container_name       = "tfstate"
      use_azuread_auth     = true
    }
  }`
	dir := writeEnvironmentOutputsFixture(t, minimalAzureEnvironmentHCL, backendValueHCL)

	env, err := LoadAzureEnvironment(dir)
	if err != nil {
		t.Fatalf("LoadAzureEnvironment failed: %v", err)
	}
	if env.Backend == nil || env.Backend.Azure == nil {
		t.Fatalf("expected env.Backend.Azure, got %+v", env.Backend)
	}
	if env.Backend.Azure.ResourceGroupName != "my-org-tfstate-rg" {
		t.Errorf("ResourceGroupName = %q, want my-org-tfstate-rg", env.Backend.Azure.ResourceGroupName)
	}
	if env.Backend.Azure.StorageAccountName != "myorgtfstate" {
		t.Errorf("StorageAccountName = %q, want myorgtfstate", env.Backend.Azure.StorageAccountName)
	}
	if env.Backend.Azure.ContainerName != "tfstate" {
		t.Errorf("ContainerName = %q, want tfstate", env.Backend.Azure.ContainerName)
	}
	if env.Backend.Azure.UseAzureADAuth == nil || *env.Backend.Azure.UseAzureADAuth != true {
		t.Errorf("UseAzureADAuth = %v, want true", env.Backend.Azure.UseAzureADAuth)
	}
}
