package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCompile_EnvironmentFlagRejectsMissingBackend confirms
// resolveEnvironmentByDefinition (reached via `compile --environment`)
// requires a `backend:` block: local-only state isn't a durable
// locator, so this entry point refuses rather than silently falling
// back the way `env init`/`env up` do. See its own doc comment and
// docs/deployment-identity-design.md.
func TestCompile_EnvironmentFlagRejectsMissingBackend(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envFile := filepath.Join(scratchDir, "environment.yaml")
	envYAML := "provider: aws\nname: demo\naws:\n  vpc_cidr: 10.0.0.0/16\n"
	if err := os.WriteFile(envFile, []byte(envYAML), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	cmd := exec.Command(bin, "compile",
		"-f", "../../../examples/hello/compose.yml",
		"--environment", envFile)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit for an environment.yaml with no backend:, got success:\n%s", out)
	}
	if !contains(string(out), "backend") {
		t.Errorf("expected the error to mention the missing backend:, got:\n%s", out)
	}
}

// TestCompile_EnvironmentFlagRejectsMissingFile confirms a clear error
// naming the path, not a generic Terraform-shaped failure, when
// --environment points at a file that doesn't exist.
func TestCompile_EnvironmentFlagRejectsMissingFile(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	missing := filepath.Join(t.TempDir(), "environment.yaml")
	cmd := exec.Command(bin, "compile",
		"-f", "../../../examples/hello/compose.yml",
		"--environment", missing)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit for a missing --environment file, got success:\n%s", out)
	}
	if !contains(string(out), "not found") {
		t.Errorf("expected a 'not found' message, got:\n%s", out)
	}
}

// TestCompile_EnvironmentFlagResolvesAndCompiles is the real
// end-to-end path: --environment given an authored environment.yaml
// with a backend: block resolves the environment (regenerating its
// Terraform directory, running `terraform init`, then reading its
// `environment`/`backend` outputs) without the caller ever needing to
// know or pass that directory's location -- unlike --env <dir>. A fake
// `terraform` on PATH stands in for both `terraform init` (a no-op
// here, real init needs real cloud credentials) and `terraform output
// -json` (answers with a fixed environment, mirroring
// env_down_test.go's own fakeTerraformThatReturnsEnvironment).
func TestCompile_EnvironmentFlagResolvesAndCompiles(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envFile := filepath.Join(scratchDir, "environment.yaml")
	envYAML := "provider: aws\nname: demo\naws:\n  vpc_cidr: 10.0.0.0/16\n" +
		"backend:\n  aws:\n    bucket: my-org-tfstate\n    region: us-east-1\n    dynamodb_table: my-org-tflock\n"
	if err := os.WriteFile(envFile, []byte(envYAML), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	fakeTerraformDir := t.TempDir()
	logFile := filepath.Join(fakeTerraformDir, "invocations.log")
	fakeTerraform := filepath.Join(fakeTerraformDir, "terraform")
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

	composeDir := t.TempDir()
	composeSrc, err := os.ReadFile("../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("read example compose.yml: %v", err)
	}
	composeFile := filepath.Join(composeDir, "compose.yml")
	if err := os.WriteFile(composeFile, composeSrc, 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	cmd := exec.Command(bin, "compile", "-f", composeFile, "--environment", envFile)
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose compile --environment failed: %v\n%s", err, out)
	}

	// The environment's own generated output directory sits next to
	// environment.yaml, exactly as `env init` would have written it --
	// resolveEnvironmentByDefinition reuses that generator, it doesn't
	// invent a separate location.
	envOutputDir := filepath.Join(scratchDir, "env-demo")
	if _, statErr := os.Stat(filepath.Join(envOutputDir, "main.tf.json")); statErr != nil {
		t.Errorf("expected the environment's own main.tf.json to have been (re)generated at %s, got: %v", envOutputDir, statErr)
	}

	// The app itself compiled successfully against the resolved
	// environment's facts (name "demo" from the fake terraform output).
	appOutputDir := filepath.Join(composeDir, "app-demo-hello", "main.tf.json")
	if _, statErr := os.Stat(appOutputDir); statErr != nil {
		t.Errorf("expected %s to exist: %v", appOutputDir, statErr)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected fake terraform to have been invoked, read log: %v", err)
	}
	if !contains(string(log), "init") {
		t.Errorf("expected a `terraform init` invocation against the environment's own directory, got:\n%s", log)
	}
	if !contains(string(log), "output") {
		t.Errorf("expected a `terraform output -json` invocation, got:\n%s", log)
	}
}
