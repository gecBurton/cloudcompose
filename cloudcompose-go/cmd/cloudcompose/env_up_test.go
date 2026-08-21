package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnvUp_Help confirms `cloud-compose env up --help` documents the
// flags this command actually reads (env up's own -e/--env and
// --auto-approve).
func TestEnvUp_Help(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "env", "up", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env up --help failed: %v\n%s", err, out)
	}

	for _, want := range []string{"-e, --env", "--auto-approve"} {
		if !contains(string(out), want) {
			t.Errorf("expected %s in cloud-compose env up --help output, got:\n%s", want, out)
		}
	}
}

// TestEnvUp_MissingEnvironmentYamlFailsWithHelpfulMessage mirrors
// TestEnvInit_MissingFileFailsWithHelpfulMessage: `env up` calls
// initEnvironment with exactly the same logic `env init` itself uses.
func TestEnvUp_MissingEnvironmentYamlFailsWithHelpfulMessage(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	missingEnvConfig := filepath.Join(scratchDir, "environment.yaml")

	cmd := exec.Command(bin, "env", "up", "-e", missingEnvConfig)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cloud-compose env up to fail for a missing environment.yaml, got:\n%s", out)
	}
	if !contains(string(out), "not found") {
		t.Errorf("expected a 'not found' message, got:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(scratchDir, "env-demo")); statErr == nil {
		t.Error("expected no env-* directory to be created when environment.yaml is missing")
	}
}

// TestEnvUp_StopsAfterApplyFails confirms `env up` reports the
// environment's own terraform apply failure clearly -- exercised via a
// `terraform` on PATH that always fails, since a real apply needs
// cloud credentials this test suite doesn't have.
func TestEnvUp_StopsAfterApplyFails(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envConfig := filepath.Join(scratchDir, "environment.yaml")
	envConfigContent := "provider: aws\nname: demo\nregion: eu-west-2\naws:\n  vpc_cidr: 10.0.0.0/16\n  az_count: 2\n  create_alb: true\n"
	if err := os.WriteFile(envConfig, []byte(envConfigContent), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	fakeTerraformDir := t.TempDir()
	fakeTerraform := filepath.Join(fakeTerraformDir, "terraform")
	if err := os.WriteFile(fakeTerraform, []byte("#!/bin/sh\necho FAKE TERRAFORM FAILURE >&2\nexit 1\n"), 0755); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}

	cmd := exec.Command(bin, "env", "up", "--env", envConfig)
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cloud-compose env up to fail when terraform apply fails, got:\n%s", out)
	}
	if !contains(string(out), "FAKE TERRAFORM FAILURE") {
		t.Errorf("expected the fake terraform's failure to surface, got:\n%s", out)
	}
}

// TestEnvUp_AutoApprovePassesFlagToTerraform confirms --auto-approve
// reaches `terraform apply` as `-auto-approve`, and that stdin is not
// attached in that mode -- exercised via a fake `terraform` on PATH
// that records its own arguments.
func TestEnvUp_AutoApprovePassesFlagToTerraform(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envConfig := filepath.Join(scratchDir, "environment.yaml")
	envConfigContent := "provider: aws\nname: demo\nregion: eu-west-2\naws:\n  vpc_cidr: 10.0.0.0/16\n  az_count: 2\n  create_alb: true\n"
	if err := os.WriteFile(envConfig, []byte(envConfigContent), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	fakeTerraformDir := t.TempDir()
	logFile := filepath.Join(fakeTerraformDir, "invocations.log")
	fakeTerraform := filepath.Join(fakeTerraformDir, "terraform")
	fakeTerraformScript := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
exit 0
`, logFile)
	if err := os.WriteFile(fakeTerraform, []byte(fakeTerraformScript), 0755); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}

	cmd := exec.Command(bin, "env", "up", "--env", envConfig, "--auto-approve")
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// No stdin attached at all -- if `env up` incorrectly tried to read
	// a confirmation prompt in --auto-approve mode, this would hang.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env up --auto-approve failed: %v\n%s", err, out)
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
	if applyCount != 1 {
		t.Errorf("expected 1 apply invocation, got %d in log:\n%s", applyCount, log)
	}
}
