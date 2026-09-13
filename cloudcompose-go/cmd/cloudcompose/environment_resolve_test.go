package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCompile_EnvironmentFlagRejectsMissingBackend confirms
// --env requires backend: to be present at all (enforced by
// initconfig.Load itself, since backend: is now a required field).
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
		"--env", envFile)
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
// --env points at a file that doesn't exist.
func TestCompile_EnvironmentFlagRejectsMissingFile(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	missing := filepath.Join(t.TempDir(), "environment.yaml")
	cmd := exec.Command(bin, "compile",
		"-f", "../../../examples/hello/compose.yml",
		"--env", missing)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit for a missing --env file, got success:\n%s", out)
	}
	if !contains(string(out), "not found") {
		t.Errorf("expected a 'not found' message, got:\n%s", out)
	}
}

// TestCompile_EnvironmentFlagRejectsUnappliedEnvironment confirms
// --env never creates or applies the environment itself:
// environment changes are a deliberate act (`env init`/`env up`), never
// a side effect of compiling/deploying an app. If <dir of
// environment.yaml>/env-<name> doesn't exist yet -- the environment
// has never even been initialized -- this must fail clearly, without
// writing anything.
func TestCompile_EnvironmentFlagRejectsUnappliedEnvironment(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envFile := filepath.Join(scratchDir, "environment.yaml")
	envYAML := "provider: aws\nname: demo\naws:\n  vpc_cidr: 10.0.0.0/16\nbackend:\n  local:\n    path: ./tfstate\n"
	if err := os.WriteFile(envFile, []byte(envYAML), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	cmd := exec.Command(bin, "compile",
		"-f", "../../../examples/hello/compose.yml",
		"--env", envFile)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit for an environment that was never initialized, got success:\n%s", out)
	}
	if !contains(string(out), "has not been applied yet") {
		t.Errorf("expected a 'has not been applied yet' message, got:\n%s", out)
	}

	envOutputDir := filepath.Join(scratchDir, "env-demo")
	if _, statErr := os.Stat(envOutputDir); statErr == nil {
		t.Errorf("expected %s to not exist -- --env must never create it as a side effect", envOutputDir)
	}
}

// TestCompile_EnvironmentFlagResolvesAndCompiles is the real
// end-to-end path: --env resolves an already-applied
// environment (env-<name> already exists, with real Terraform outputs)
// without the caller needing to pass its directory directly. A fake
// `terraform` on PATH stands in for `terraform output -json`; unlike
// before, --env itself never runs `terraform init` or rewrites
// main.tf.json -- env-<name> is set up here exactly as `env init`
// would have left it, to isolate that --env only reads it.
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

	// env-demo pre-exists, as if `env init` (and `terraform apply`) had
	// already run there -- --env must find and read this, not
	// create its own.
	envOutputDir := filepath.Join(scratchDir, "env-demo")
	if err := os.MkdirAll(envOutputDir, 0755); err != nil {
		t.Fatalf("mkdir env-demo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envOutputDir, "main.tf.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("write main.tf.json: %v", err)
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

	cmd := exec.Command(bin, "compile", "-f", composeFile, "--env", envFile)
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose compile --env failed: %v\n%s", err, out)
	}

	appOutputDir := filepath.Join(composeDir, "app-demo-hello", "main.tf.json")
	if _, statErr := os.Stat(appOutputDir); statErr != nil {
		t.Errorf("expected %s to exist: %v", appOutputDir, statErr)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected fake terraform to have been invoked, read log: %v", err)
	}
	if !contains(string(log), "output") {
		t.Errorf("expected a `terraform output -json` invocation, got:\n%s", log)
	}
	if contains(string(log), "init") {
		t.Errorf("expected no `terraform init` invocation -- --env must never modify the environment's own directory, got:\n%s", log)
	}
}

// TestCompile_EnvironmentFlagResolvesLocalBackend mirrors
// TestCompile_EnvironmentFlagResolvesAndCompiles for a local: backend,
// confirming --env works identically for both.
func TestCompile_EnvironmentFlagResolvesLocalBackend(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envFile := filepath.Join(scratchDir, "environment.yaml")
	envYAML := "provider: aws\nname: demo\naws:\n  vpc_cidr: 10.0.0.0/16\nbackend:\n  local:\n    path: ./tfstate\n"
	if err := os.WriteFile(envFile, []byte(envYAML), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	envOutputDir := filepath.Join(scratchDir, "env-demo")
	if err := os.MkdirAll(envOutputDir, 0755); err != nil {
		t.Fatalf("mkdir env-demo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envOutputDir, "main.tf.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("write main.tf.json: %v", err)
	}

	fakeTerraformDir := t.TempDir()
	fakeTerraform := filepath.Join(fakeTerraformDir, "terraform")
	fakeTerraformScript := `#!/bin/sh
if [ "$1" = "output" ]; then
  echo '{"environment": {"value": {"target": "aws", "name": "demo", "vpc_id": "vpc-1", "public_subnets": ["s1"], "private_subnets": ["s2"], "ecs_cluster_arn": "arn:aws:ecs:x"}}}'
fi
exit 0
`
	if err := os.WriteFile(fakeTerraform, []byte(fakeTerraformScript), 0755); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}

	cmd := exec.Command(bin, "compile",
		"-f", "../../../examples/hello/compose.yml",
		"--env", envFile)
	cmd.Env = append(os.Environ(), "PATH="+fakeTerraformDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose compile --env with a local backend failed: %v\n%s", err, out)
	}
}
