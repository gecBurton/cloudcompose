package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestUp_Help confirms `cloudcompose up --help` documents the flags this
// command actually reads (compile.go's -f, plus up's own --env-config,
// -p, --subnet-index, --auto-approve) -- see up.go's own doc comment for
// why --auto-approve exists (non-interactive callers) and is off by
// default.
func TestUp_Help(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "up", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloudcompose up --help failed: %v\n%s", err, out)
	}

	for _, want := range []string{"-f, --file", "--env-config", "-p, --project", "--subnet-index", "--auto-approve"} {
		if !contains(string(out), want) {
			t.Errorf("expected %s in cloudcompose up --help output, got:\n%s", want, out)
		}
	}
}

// TestUp_MissingEnvironmentYamlFailsWithHelpfulMessage mirrors
// TestInit_MissingFileFailsWithHelpfulMessage: `up` calls initEnvironment
// with exactly the same logic `init` itself uses, so a missing
// environment.yaml should fail the same clear way, before ever attempting
// to run terraform (which would otherwise be the more confusing point to
// fail at).
func TestUp_MissingEnvironmentYamlFailsWithHelpfulMessage(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	missingEnvConfig := filepath.Join(scratchDir, "environment.yaml")
	composeFile := filepath.Join(scratchDir, "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  web:\n    image: nginx\n"), 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	cmd := exec.Command(bin, "up", "-f", composeFile, "--env-config", missingEnvConfig)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cloudcompose up to fail for a missing environment.yaml, got:\n%s", out)
	}
	if !contains(string(out), "not found") {
		t.Errorf("expected a 'not found' message, got:\n%s", out)
	}
	// Should fail before ever invoking terraform -- no directory should
	// have been created for a nonexistent config.
	if _, statErr := os.Stat(filepath.Join(scratchDir, "env-demo")); statErr == nil {
		t.Error("expected no env-* directory to be created when environment.yaml is missing")
	}
}

// TestUp_StopsAfterEnvironmentApplyFails confirms `up` doesn't proceed to
// compile/apply the app if the environment's own terraform apply fails --
// exercised here via a `terraform` on PATH that always fails, since a
// real apply needs cloud credentials this test suite doesn't have. If
// `up` incorrectly ignored the environment apply's failure, this test
// would see app-demo/main.tf.json get written anyway.
func TestUp_StopsAfterEnvironmentApplyFails(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envConfig := filepath.Join(scratchDir, "environment.yaml")
	envConfigContent := "provider: aws\nname: demo\nregion: eu-west-2\naws:\n  vpc_cidr: 10.0.0.0/16\n  az_count: 2\n  create_alb: true\n"
	if err := os.WriteFile(envConfig, []byte(envConfigContent), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}
	composeFile := filepath.Join(scratchDir, "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  web:\n    image: nginx\n    ports:\n      - 80:80\n"), 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	fakeTerraformDir := t.TempDir()
	fakeTerraform := filepath.Join(fakeTerraformDir, "terraform")
	if err := os.WriteFile(fakeTerraform, []byte("#!/bin/sh\necho FAKE TERRAFORM FAILURE >&2\nexit 1\n"), 0755); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}

	cmd := exec.Command(bin, "up", "-f", composeFile, "--env-config", envConfig)
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cloudcompose up to fail when terraform apply fails, got:\n%s", out)
	}
	if !contains(string(out), "FAKE TERRAFORM FAILURE") {
		t.Errorf("expected the fake terraform's failure to surface, got:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(scratchDir, "app-demo", "main.tf.json")); statErr == nil {
		t.Error("expected cloudcompose up to stop before compiling the app when the environment apply fails")
	}
}

// TestUp_AutoApprovePassesFlagToTerraform confirms --auto-approve
// reaches both `terraform apply` invocations (environment and app) as
// `-auto-approve`, and that neither step is passed a real stdin in that
// mode -- exercised via a fake `terraform` on PATH that records its own
// arguments, since a real apply needs cloud credentials this test suite
// doesn't have.
func TestUp_AutoApprovePassesFlagToTerraform(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envConfig := filepath.Join(scratchDir, "environment.yaml")
	envConfigContent := "provider: aws\nname: demo\nregion: eu-west-2\naws:\n  vpc_cidr: 10.0.0.0/16\n  az_count: 2\n  create_alb: true\n"
	if err := os.WriteFile(envConfig, []byte(envConfigContent), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}
	composeFile := filepath.Join(scratchDir, "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  web:\n    image: nginx\n    ports:\n      - 80:80\n"), 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	fakeTerraformDir := t.TempDir()
	logFile := filepath.Join(fakeTerraformDir, "invocations.log")
	fakeTerraform := filepath.Join(fakeTerraformDir, "terraform")
	// Answers `terraform output -json` for real (the app compile step's
	// LoadEnvironment call needs it) while logging every invocation's
	// arguments, so this test can assert on what `up` actually passed
	// terraform without needing real cloud credentials for the applies
	// themselves.
	fakeTerraformScript := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
if [ "$1" = "output" ]; then
  echo '{"environment": {"value": {"target": "aws", "name": "demo", "vpc_id": "vpc-1", "public_subnets": ["s1"], "private_subnets": ["s2"], "ecs_cluster_arn": "arn:aws:ecs:x"}}}'
fi
exit 0
`, logFile)
	if err := os.WriteFile(fakeTerraform, []byte(fakeTerraformScript), 0755); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}

	cmd := exec.Command(bin, "up", "-f", composeFile, "--env-config", envConfig, "--auto-approve")
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// No stdin attached at all -- if `up` incorrectly tried to read a
	// confirmation prompt in --auto-approve mode, this would hang (or, in
	// CombinedOutput's case, read from an already-closed pipe) rather
	// than an assertion failure, which is exactly the failure mode
	// --auto-approve exists to avoid for non-interactive callers.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloudcompose up --auto-approve failed: %v\n%s", err, out)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected fake terraform to have been invoked, read log: %v", err)
	}
	applyCount := 0
	for _, line := range strings.Split(strings.TrimRight(string(log), "\n"), "\n") {
		if contains(line, "apply") {
			applyCount++
			if !contains(line, "-auto-approve") {
				t.Errorf("expected -auto-approve on every apply invocation, got: %q", line)
			}
		}
	}
	if applyCount != 2 {
		t.Errorf("expected 2 apply invocations (environment + app), got %d in log:\n%s", applyCount, log)
	}
}
