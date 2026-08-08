package compiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeTerraformOutputsFixture creates a scratch directory containing
// only a single `output "environment"` block (no providers, no
// resources) declaring value as its literal value, then runs
// `terraform init`/`apply` in it so LoadEnvironment's
// `terraform output -json` call has real state to read. No network
// access needed: a config with zero providers has nothing to fetch, so
// `terraform init` completes offline.
func writeTerraformOutputsFixture(t *testing.T, valueHCL string) string {
	t.Helper()
	dir := t.TempDir()

	mainTF := fmt.Sprintf(`output "environment" {
  value = %s
}
`, valueHCL)
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

// TestLoadEnvironment_DispatchesOnTarget mirrors environment.py's
// load_environment: dispatching to the right cloud-specific loader based
// on the declared target field, now read from a real (offline, no
// provider) Terraform state's `environment` output rather than a hand-
// written YAML file -- see docs/authored-environment-config.md for why
// LoadEnvironment reads live Terraform state instead.
func TestLoadEnvironment_DispatchesOnTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		valueHCL string
		wantType string
	}{
		{
			name: "aws",
			valueHCL: `{
    target                 = "aws"
    name                   = "prod"
    vpc_id                 = "vpc-1"
    public_subnets         = ["s1"]
    private_subnets        = ["s2"]
    ecs_cluster_arn        = "arn:aws:ecs:x"
  }`,
			wantType: "*models.AwsEnvironment",
		},
		{
			name: "azure",
			valueHCL: `{
    target                           = "azure"
    name                             = "prod"
    container_apps_environment_name  = "env"
    log_analytics_workspace_id       = "x"
    vnet_id                          = "y"
    infrastructure_subnet_id         = "z"
  }`,
			wantType: "*models.AzureEnvironment",
		},
		{
			name: "gcp",
			valueHCL: `{
    target     = "gcp"
    name       = "prod"
    project_id = "my-project"
  }`,
			wantType: "*models.GcpEnvironment",
		},
		{
			// No target declared -> defaults to aws, matching
			// environment.py's DEFAULT_TARGET = "aws".
			name: "default-to-aws",
			valueHCL: `{
    name             = "prod"
    vpc_id           = "vpc-1"
    public_subnets   = ["s1"]
    private_subnets  = ["s2"]
    ecs_cluster_arn  = "arn:aws:ecs:x"
  }`,
			wantType: "*models.AwsEnvironment",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTerraformOutputsFixture(t, tc.valueHCL)

			env, err := LoadEnvironment(dir)
			if err != nil {
				t.Fatalf("LoadEnvironment failed: %v", err)
			}
			gotType := fmt.Sprintf("%T", env)
			if gotType != tc.wantType {
				t.Errorf("got type %s, want %s", gotType, tc.wantType)
			}
		})
	}
}

// TestLoadEnvironment_RejectsUnsupportedTarget mirrors load_environment's
// error path for a declared target outside {aws, azure, gcp}.
func TestLoadEnvironment_RejectsUnsupportedTarget(t *testing.T) {
	t.Parallel()
	dir := writeTerraformOutputsFixture(t, `{
    target = "openstack"
    name   = "prod"
  }`)

	_, err := LoadEnvironment(dir)
	if err == nil {
		t.Fatal("expected an error for an unsupported target")
	}
}
