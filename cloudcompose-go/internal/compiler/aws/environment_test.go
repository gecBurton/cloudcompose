package aws

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeEnvironmentOutputsFixture creates a scratch directory containing
// only two Terraform outputs -- "environment" and, if backendValueHCL
// is non-empty, "backend" -- then runs `terraform init`/`apply` in it,
// mirroring internal/compiler/environment_test.go's own
// writeTerraformOutputsFixture (kept as a separate, package-local copy
// rather than exported/shared, since each package's own fixture only
// ever needs its own cloud's exact output shape). No network access
// needed: a config with zero providers has nothing to fetch.
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

const minimalAwsEnvironmentHCL = `{
    target                 = "aws"
    name                   = "prod"
    vpc_id                 = "vpc-1"
    public_subnets         = ["s1"]
    private_subnets        = ["s2"]
    ecs_cluster_arn        = "arn:aws:ecs:x"
  }`

// TestLoadAwsEnvironment_NoBackendOutputLeavesBackendNil confirms an
// environment applied without a `backend` output at all (today's
// default -- see docs/multi-user-state.md) leaves env.Backend nil,
// rather than LoadAwsEnvironment erroring on the missing output.
func TestLoadAwsEnvironment_NoBackendOutputLeavesBackendNil(t *testing.T) {
	t.Parallel()
	dir := writeEnvironmentOutputsFixture(t, minimalAwsEnvironmentHCL, "")

	env, err := LoadAwsEnvironment(dir)
	if err != nil {
		t.Fatalf("LoadAwsEnvironment failed: %v", err)
	}
	if env.Backend != nil {
		t.Errorf("expected nil Backend, got %+v", env.Backend)
	}
}

// TestLoadAwsEnvironment_DecodesBackendOutput confirms a real,
// end-to-end roundtrip: the exact shape
// aws.GenerateAwsEnvironment's own `output "backend"` block writes
// (see its own doc comment) decodes back into env.Backend.AWS with the
// same bucket/region/dynamodb_table facts, via a real
// `terraform output -json` call, not a hand-built map.
func TestLoadAwsEnvironment_DecodesBackendOutput(t *testing.T) {
	t.Parallel()
	backendValueHCL := `{
    provider = "aws"
    aws = {
      bucket         = "my-org-tfstate"
      region         = "eu-west-2"
      dynamodb_table = "my-org-tflocks"
    }
  }`
	dir := writeEnvironmentOutputsFixture(t, minimalAwsEnvironmentHCL, backendValueHCL)

	env, err := LoadAwsEnvironment(dir)
	if err != nil {
		t.Fatalf("LoadAwsEnvironment failed: %v", err)
	}
	if env.Backend == nil || env.Backend.AWS == nil {
		t.Fatalf("expected env.Backend.AWS, got %+v", env.Backend)
	}
	if env.Backend.AWS.Bucket != "my-org-tfstate" {
		t.Errorf("Bucket = %q, want my-org-tfstate", env.Backend.AWS.Bucket)
	}
	if env.Backend.AWS.Region != "eu-west-2" {
		t.Errorf("Region = %q, want eu-west-2", env.Backend.AWS.Region)
	}
	if env.Backend.AWS.DynamoDBTable != "my-org-tflocks" {
		t.Errorf("DynamoDBTable = %q, want my-org-tflocks", env.Backend.AWS.DynamoDBTable)
	}
}
