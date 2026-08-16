package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDown_Help confirms `cloudcompose down --help` documents the flags
// this command actually reads (compose_file.go's global -f, plus
// down's own -e/--env and --auto-approve) -- see down.go's own doc
// comment for why --auto-approve exists (non-interactive callers) and
// is off by default.
func TestDown_Help(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "down", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloudcompose down --help failed: %v\n%s", err, out)
	}

	for _, want := range []string{"-f, --file", "-e, --env", "--auto-approve"} {
		if !contains(string(out), want) {
			t.Errorf("expected %s in cloudcompose down --help output, got:\n%s", want, out)
		}
	}
}

// TestDown_RequiresEnv confirms `cloudcompose down` has no default
// environment, matching `ps`'s/`logs`'s own requirement.
func TestDown_RequiresEnv(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "down", "-f", "../../../examples/hello/compose.yml")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit when --env is not given, got success:\n%s", out)
	}
	if !contains(string(out), "--env is required") {
		t.Errorf("expected the error to name --env, got:\n%s", out)
	}
}

// writeAwsEnvironmentFixture creates a scratch directory containing
// only a single `output "environment"` block (no providers, no
// resources) declaring a minimal-but-valid AWS environment, then runs
// `terraform init`/`apply` in it so LoadEnvironment's own `terraform
// output -json` call has real state to read -- mirrors
// internal/compiler/environment_test.go's writeTerraformOutputsFixture,
// duplicated here rather than exported cross-package since it's a test
// helper, not part of either package's public API. No network access
// needed: a config with zero providers has nothing to fetch, so
// `terraform init` completes offline.
func writeAwsEnvironmentFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()

	mainTF := fmt.Sprintf(`output "environment" {
  value = {
    target          = "aws"
    name            = %q
    vpc_id          = "vpc-1"
    public_subnets  = ["s1"]
    private_subnets = ["s2"]
    ecs_cluster_arn = "arn:aws:ecs:x"
  }
}
`, name)
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

// TestDown_FailsWhenAppNeverCompiled confirms `down` fails clearly, and
// never invokes terraform destroy at all, when the app-<environment
// name> directory a previous `cloudcompose compile` would have written
// doesn't exist -- rather than terraform itself producing a more
// confusing "no configuration files" error.
func TestDown_FailsWhenAppNeverCompiled(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envDir := writeAwsEnvironmentFixture(t, "demo")
	composeFile := filepath.Join(scratchDir, "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  web:\n    image: nginx\n"), 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	cmd := exec.Command(bin, "down", "-f", composeFile, "-e", envDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cloudcompose down to fail when the app was never compiled, got:\n%s", out)
	}
	if !contains(string(out), "does not exist") {
		t.Errorf("expected a 'does not exist' message, got:\n%s", out)
	}
}

// TestDown_RunsTerraformDestroyInAppDir confirms `down` resolves the
// same app-<environment name> directory `compile` writes to and runs
// `terraform destroy` there -- exercised via a fake `terraform` on PATH
// that records its own working directory and arguments once init.go's
// real environment lookup (via the offline fixture above) has already
// resolved the app directory, since a real destroy needs cloud
// credentials this test suite doesn't have.
func TestDown_RunsTerraformDestroyInAppDir(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	envDir := writeAwsEnvironmentFixture(t, "demo")
	appDir := filepath.Join(filepath.Dir(envDir), "app-demo")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir app-demo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "main.tf.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("write main.tf.json: %v", err)
	}
	composeFile := filepath.Join(filepath.Dir(envDir), "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  web:\n    image: nginx\n    ports:\n      - 80:80\n"), 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	fakeTerraformDir := t.TempDir()
	logFile := filepath.Join(fakeTerraformDir, "invocations.log")
	fakeTerraform := filepath.Join(fakeTerraformDir, "terraform")
	// down also needs to resolve envDir's own `environment` output (to
	// learn its name, "demo", and build app-demo) before it ever runs
	// terraform in appDir -- since the fake terraform below intercepts
	// every invocation on PATH, including that one, it has to answer
	// `terraform output -json` for real rather than just logging it.
	fakeTerraformScript := fmt.Sprintf(`#!/bin/sh
echo "$PWD $@" >> %s
if [ "$1" = "output" ]; then
  echo '{"environment": {"value": {"target": "aws", "name": "demo", "vpc_id": "vpc-1", "public_subnets": ["s1"], "private_subnets": ["s2"], "ecs_cluster_arn": "arn:aws:ecs:x"}}}'
fi
exit 0
`, logFile)
	if err := os.WriteFile(fakeTerraform, []byte(fakeTerraformScript), 0755); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}

	cmd := exec.Command(bin, "down", "-f", composeFile, "-e", envDir)
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloudcompose down failed: %v\n%s", err, out)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected fake terraform to have been invoked, read log: %v", err)
	}
	if !contains(string(log), appDir) {
		t.Errorf("expected terraform to run in %s, got invocations:\n%s", appDir, log)
	}
	if !contains(string(log), "destroy") {
		t.Errorf("expected a `terraform destroy` invocation, got:\n%s", log)
	}
	if contains(string(log), "-auto-approve") {
		t.Error("expected no -auto-approve by default; destroy must stay interactive unless --auto-approve is given")
	}
}

// TestDown_AutoApprovePassesFlagToTerraform confirms --auto-approve
// reaches `terraform destroy` as `-auto-approve` -- exercised via a fake
// `terraform` on PATH that records its own arguments, since a real
// destroy needs cloud credentials this test suite doesn't have.
func TestDown_AutoApprovePassesFlagToTerraform(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	envDir := writeAwsEnvironmentFixture(t, "demo")
	appDir := filepath.Join(filepath.Dir(envDir), "app-demo")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir app-demo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "main.tf.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("write main.tf.json: %v", err)
	}
	composeFile := filepath.Join(filepath.Dir(envDir), "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  web:\n    image: nginx\n    ports:\n      - 80:80\n"), 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	fakeTerraformDir := t.TempDir()
	logFile := filepath.Join(fakeTerraformDir, "invocations.log")
	fakeTerraform := filepath.Join(fakeTerraformDir, "terraform")
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

	cmd := exec.Command(bin, "down", "-f", composeFile, "-e", envDir, "--auto-approve")
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// No stdin attached at all -- see up_test.go's identical note on why
	// this matters for --auto-approve specifically.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloudcompose down --auto-approve failed: %v\n%s", err, out)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected fake terraform to have been invoked, read log: %v", err)
	}
	if !contains(string(log), "destroy -auto-approve") {
		t.Errorf("expected `terraform destroy -auto-approve`, got invocations:\n%s", log)
	}
}
