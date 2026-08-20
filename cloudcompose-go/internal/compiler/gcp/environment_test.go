package gcp

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

const minimalGcpEnvironmentHCL = `{
    target     = "gcp"
    name       = "prod"
    project_id = "my-project"
  }`

// TestLoadGcpEnvironment_NoBackendOutputLeavesBackendNil mirrors
// aws.TestLoadAwsEnvironment_NoBackendOutputLeavesBackendNil.
func TestLoadGcpEnvironment_NoBackendOutputLeavesBackendNil(t *testing.T) {
	t.Parallel()
	dir := writeEnvironmentOutputsFixture(t, minimalGcpEnvironmentHCL, "")

	env, err := LoadGcpEnvironment(dir)
	if err != nil {
		t.Fatalf("LoadGcpEnvironment failed: %v", err)
	}
	if env.Backend != nil {
		t.Errorf("expected nil Backend, got %+v", env.Backend)
	}
}

// TestLoadGcpEnvironment_DecodesBackendOutput mirrors
// aws.TestLoadAwsEnvironment_DecodesBackendOutput: a real,
// end-to-end roundtrip of gcp.GenerateGcpEnvironment's own
// `output "backend"` shape back into env.Backend.Gcp.
func TestLoadGcpEnvironment_DecodesBackendOutput(t *testing.T) {
	t.Parallel()
	backendValueHCL := `{
    provider = "gcp"
    gcp = {
      bucket = "my-org-tfstate"
    }
  }`
	dir := writeEnvironmentOutputsFixture(t, minimalGcpEnvironmentHCL, backendValueHCL)

	env, err := LoadGcpEnvironment(dir)
	if err != nil {
		t.Fatalf("LoadGcpEnvironment failed: %v", err)
	}
	if env.Backend == nil || env.Backend.Gcp == nil {
		t.Fatalf("expected env.Backend.Gcp, got %+v", env.Backend)
	}
	if env.Backend.Gcp.Bucket != "my-org-tfstate" {
		t.Errorf("Bucket = %q, want my-org-tfstate", env.Backend.Gcp.Bucket)
	}
}
