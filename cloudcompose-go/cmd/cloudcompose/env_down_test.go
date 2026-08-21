package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cleanAWSEnv returns env with every AWS_*/credential-related variable
// removed, so aws.NewS3Client (env_down.go's own
// checkNoDependentApps) has no ambient credentials at all to find --
// used to exercise the "check itself can't run" degrade-to-warning path
// deterministically, without depending on whatever happens to be set in
// the environment this test suite runs in.
func cleanAWSEnv(env []string) []string {
	var cleaned []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "AWS_") {
			continue
		}
		cleaned = append(cleaned, kv)
	}
	return cleaned
}

// TestEnvDown_Help confirms `cloud-compose env down --help`
// documents the flags this command actually reads (compose_file.go's
// global -f is not used by this command at all -- env down has no
// compose file input -- plus env down's own -e/--env, --force, and
// --auto-approve).
func TestEnvDown_Help(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "env", "down", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env down --help failed: %v\n%s", err, out)
	}

	for _, want := range []string{"-e, --env", "--force", "--auto-approve"} {
		if !contains(string(out), want) {
			t.Errorf("expected %s in cloud-compose env down --help output, got:\n%s", want, out)
		}
	}
}

// TestEnvDown_RequiresEnv mirrors TestComposeDown_RequiresEnv.
func TestEnvDown_RequiresEnv(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "env", "down")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit when --env is not given, got success:\n%s", out)
	}
	if !contains(string(out), "--env is required") {
		t.Errorf("expected the error to name --env, got:\n%s", out)
	}
}

// TestEnvDown_FailsWhenEnvDirDoesNotExist mirrors
// TestComposeDown_FailsWhenAppNeverCompiled's own "clear error, never invokes
// terraform" rationale, for a --env directory that doesn't exist at all
// (as opposed to down's app directory).
func TestEnvDown_FailsWhenEnvDirDoesNotExist(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "env", "down", "-e", filepath.Join(t.TempDir(), "does-not-exist"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cloud-compose env down to fail for a nonexistent --env, got:\n%s", out)
	}
	if !contains(string(out), "does not exist") {
		t.Errorf("expected a 'does not exist' message, got:\n%s", out)
	}
}

// writeAwsEnvironmentFixtureWithBackend mirrors compose_down_test.go's own
// writeAwsEnvironmentFixture, but also declares a `backend` output --
// see aws.GenerateAwsEnvironment's own doc comment for the shape every
// environment_generator.go writes there, and
// docs/multi-user-state.md for why LoadEnvironment decodes it into
// env.Backend.
func writeAwsEnvironmentFixtureWithBackend(t *testing.T, name string) string {
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

output "backend" {
  value = {
    provider = "aws"
    aws = {
      bucket = "my-org-tfstate"
      region = "us-east-1"
    }
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

// fakeTerraformThatReturnsEnvironment writes a fake `terraform`
// executable to a scratch directory that answers `terraform output
// -json` with environmentJSON (verbatim) and otherwise just logs its
// own invocation and exits 0 -- mirroring compose_down_test.go's own inline
// fake terraform scripts, extracted here since env_down_test.go
// needs the same shape more than once.
func fakeTerraformThatReturnsEnvironment(t *testing.T, environmentJSON string) (dir, logFile string) {
	t.Helper()
	dir = t.TempDir()
	logFile = filepath.Join(dir, "invocations.log")
	fakeTerraform := filepath.Join(dir, "terraform")
	script := fmt.Sprintf(`#!/bin/sh
echo "$PWD $@" >> %s
if [ "$1" = "output" ]; then
  echo '%s'
fi
exit 0
`, logFile, environmentJSON)
	if err := os.WriteFile(fakeTerraform, []byte(script), 0755); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}
	return dir, logFile
}

// TestEnvDown_NoBackendConfiguredWarnsAndProceeds confirms an
// environment with no backend: configured (writeAwsEnvironmentFixture,
// compose_down_test.go's own helper, declares no `backend` output at all) skips
// the dependent-app check with a warning and still runs `terraform
// destroy` -- see envDownCmd's own doc comment for why this can't
// block teardown the same way finding a real dependent app does.
func TestEnvDown_NoBackendConfiguredWarnsAndProceeds(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	envDir := writeAwsEnvironmentFixture(t, "demo")

	fakeTerraformDir, logFile := fakeTerraformThatReturnsEnvironment(t, `{"environment": {"value": {"target": "aws", "name": "demo", "vpc_id": "vpc-1", "public_subnets": ["s1"], "private_subnets": ["s2"], "ecs_cluster_arn": "arn:aws:ecs:x"}}}`)

	cmd := exec.Command(bin, "env", "down", "-e", envDir, "--auto-approve")
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env down failed: %v\n%s", err, out)
	}
	if !contains(string(out), "no backend configured") {
		t.Errorf("expected a 'no backend configured' warning, got:\n%s", out)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected fake terraform to have been invoked, read log: %v", err)
	}
	if !contains(string(log), envDir) || !contains(string(log), "destroy") {
		t.Errorf("expected a `terraform destroy` invocation in %s, got:\n%s", envDir, log)
	}
}

// TestEnvDown_BackendConfiguredButUnreachableWarnsAndProceeds
// confirms an environment with a backend configured, but whose list
// call can't actually run in this test environment (no real AWS
// credentials), still degrades to a warning rather than blocking
// teardown -- see checkNoDependentApps' own doc comment for why every
// way this check can fail to run must be treated the same way.
func TestEnvDown_BackendConfiguredButUnreachableWarnsAndProceeds(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	envDir := writeAwsEnvironmentFixtureWithBackend(t, "demo")

	fakeTerraformDir, logFile := fakeTerraformThatReturnsEnvironment(t, `{"environment": {"value": {"target": "aws", "name": "demo", "vpc_id": "vpc-1", "public_subnets": ["s1"], "private_subnets": ["s2"], "ecs_cluster_arn": "arn:aws:ecs:x"}}, "backend": {"value": {"provider": "aws", "aws": {"bucket": "my-org-tfstate", "region": "us-east-1"}}}}`)

	// Deliberately clear every AWS credential env var so
	// aws.NewS3Client fails to load a config, exercising the
	// "check itself can't run at all" path rather than a real,
	// permission-denied list call (which would need a real AWS
	// account).
	cmd := exec.Command(bin, "env", "down", "-e", envDir, "--auto-approve")
	cmd.Env = append(cleanAWSEnv(os.Environ()), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env down failed: %v\n%s", err, out)
	}
	if !contains(string(out), "could not check for dependent apps") {
		t.Errorf("expected a 'could not check for dependent apps' warning, got:\n%s", out)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected fake terraform to have been invoked, read log: %v", err)
	}
	if !contains(string(log), "destroy") {
		t.Errorf("expected a `terraform destroy` invocation, got:\n%s", log)
	}
}

// TestEnvDown_ForceSkipsCheckEntirely confirms --force runs
// terraform destroy without printing any dependent-app-check warning at
// all -- the escape hatch envDownCmd's own doc comment describes for
// when the check itself can't run, or an operator has already confirmed
// by other means.
func TestEnvDown_ForceSkipsCheckEntirely(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	envDir := writeAwsEnvironmentFixtureWithBackend(t, "demo")

	fakeTerraformDir, logFile := fakeTerraformThatReturnsEnvironment(t, `{"environment": {"value": {"target": "aws", "name": "demo", "vpc_id": "vpc-1", "public_subnets": ["s1"], "private_subnets": ["s2"], "ecs_cluster_arn": "arn:aws:ecs:x"}}, "backend": {"value": {"provider": "aws", "aws": {"bucket": "my-org-tfstate", "region": "us-east-1"}}}}`)

	cmd := exec.Command(bin, "env", "down", "-e", envDir, "--force", "--auto-approve")
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env down --force failed: %v\n%s", err, out)
	}
	if contains(string(out), "dependent apps") {
		t.Errorf("expected --force to skip the dependent-app check entirely, got:\n%s", out)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected fake terraform to have been invoked, read log: %v", err)
	}
	if !contains(string(log), "destroy") {
		t.Errorf("expected a `terraform destroy` invocation, got:\n%s", log)
	}
}

// TestEnvDown_NoAutoApproveDoesNotPassFlag mirrors compose_down_test.go's own
// TestComposeDown_RunsTerraformDestroyInAppDir "-auto-approve absent by
// default" assertion.
func TestEnvDown_NoAutoApproveDoesNotPassFlag(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	envDir := writeAwsEnvironmentFixture(t, "demo")
	fakeTerraformDir, logFile := fakeTerraformThatReturnsEnvironment(t, `{"environment": {"value": {"target": "aws", "name": "demo", "vpc_id": "vpc-1", "public_subnets": ["s1"], "private_subnets": ["s2"], "ecs_cluster_arn": "arn:aws:ecs:x"}}}`)

	cmd := exec.Command(bin, "env", "down", "-e", envDir, "--force")
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// No stdin attached -- terraform destroy would block on a prompt
	// forever without --auto-approve; the fake terraform script never
	// actually prompts, so this just confirms the flag isn't passed,
	// mirroring compose_down_test.go's own reasoning.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env down failed: %v\n%s", err, out)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected fake terraform to have been invoked, read log: %v", err)
	}
	if contains(string(log), "-auto-approve") {
		t.Error("expected no -auto-approve by default; destroy must stay interactive unless --auto-approve is given")
	}
}
