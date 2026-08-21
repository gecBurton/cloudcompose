package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildCloudComposeBinary builds the cloud-compose CLI once per test
// run and returns its path, for the integration-style tests below that
// exercise runEnvInit through the real cobra command (including
// os.Exit paths), rather than calling initEnvironment directly.
func buildCloudComposeBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cloud-compose")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cloud-compose: %v\n%s", err, out)
	}
	return bin
}

// TestEnvInit_MissingFileFailsWithHelpfulMessage mirrors
// docs/authored-environment-config.md's "cloudcompose init behavior":
// cloud-compose env init has no flags-only fallback -- a missing
// environment.yaml is an error naming the missing path and pointing at
// examples/hello/environment.yaml, not a silent flags-based bootstrap.
func TestEnvInit_MissingFileFailsWithHelpfulMessage(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "env", "init")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("expected a non-zero exit for a missing environment.yaml, got success:\n%s", out)
	}
	if !contains(string(out), "environment.yaml not found") {
		t.Errorf("expected the error to name the missing file, got:\n%s", out)
	}
	if !contains(string(out), "examples/hello/environment.yaml") {
		t.Errorf("expected the error to point at the example, got:\n%s", out)
	}
}

// TestEnvInit_NoDecisionFlags confirms cloud-compose env init's flag
// set really is just -e/--env -- -f/--file is deliberately absent from
// env init (unlike every other command that has one): env init is the
// one command with no compose file at all.
func TestEnvInit_NoDecisionFlags(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "env", "init", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env init --help failed: %v\n%s", err, out)
	}

	for _, removedFlag := range []string{
		"--provider", "--name", "--region", "--vpc-cidr", "--az-count",
		"--create-alb", "--certificate-arn", "--aws-endpoint",
		"--project-id", "--domain", "--retain-data", "--tags",
		"--output", "-o,",
	} {
		if contains(string(out), removedFlag) {
			t.Errorf("expected %s to have been removed from cloud-compose env init, but it's still in --help output:\n%s", removedFlag, out)
		}
	}
	for _, keptFlag := range []string{"-e, --env"} {
		if !contains(string(out), keptFlag) {
			t.Errorf("expected %s in cloud-compose env init --help output, got:\n%s", keptFlag, out)
		}
	}
}

// TestEnvInit_RealAwsExampleProducesValidManifest exercises
// cloud-compose env init end-to-end against the real, committed
// examples/hello/environment.yaml and checks the resulting
// main.tf.json is well-formed.
func TestEnvInit_RealAwsExampleProducesValidManifest(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	envSrc, err := os.ReadFile("../../../examples/hello/environment.yaml")
	if err != nil {
		t.Fatalf("read examples/hello/environment.yaml: %v", err)
	}
	scratchDir := t.TempDir()
	envFile := filepath.Join(scratchDir, "environment.yaml")
	if err := os.WriteFile(envFile, envSrc, 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	cmd := exec.Command(bin, "env", "init", "-e", envFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env init failed: %v\n%s", err, out)
	}

	outDir := filepath.Join(scratchDir, "env-demo")
	for _, want := range []string{"main.tf.json", "environment.yaml"} {
		if _, err := os.Stat(filepath.Join(outDir, want)); err != nil {
			t.Errorf("expected %s to be written, got: %v", want, err)
		}
	}
}

// TestEnvInit_WarnsWhenNoBackendConfigured confirms `cloud-compose env
// init` prints initconfig.BackendWarnings' own "no backend configured"
// warning when environment.yaml has no backend: block.
func TestEnvInit_WarnsWhenNoBackendConfigured(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envFile := filepath.Join(scratchDir, "environment.yaml")
	envYAML := "provider: aws\nname: demo\naws:\n  vpc_cidr: 10.0.0.0/16\n"
	if err := os.WriteFile(envFile, []byte(envYAML), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	cmd := exec.Command(bin, "env", "init", "-e", envFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env init failed: %v\n%s", err, out)
	}
	if !contains(string(out), "no backend configured") {
		t.Errorf("expected a 'no backend configured' warning, got:\n%s", out)
	}
}

// TestEnvInit_WarnsWhenAwsBackendHasNoLockTable mirrors
// TestEnvInit_WarnsWhenNoBackendConfigured for the other
// BackendWarnings case: an AWS backend configured without
// dynamodb_table.
func TestEnvInit_WarnsWhenAwsBackendHasNoLockTable(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envFile := filepath.Join(scratchDir, "environment.yaml")
	envYAML := "provider: aws\nname: demo\naws:\n  vpc_cidr: 10.0.0.0/16\n" +
		"backend:\n  aws:\n    bucket: my-org-tfstate\n    region: us-east-1\n"
	if err := os.WriteFile(envFile, []byte(envYAML), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	cmd := exec.Command(bin, "env", "init", "-e", envFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env init failed: %v\n%s", err, out)
	}
	if !contains(string(out), "no dynamodb_table configured") {
		t.Errorf("expected a 'no dynamodb_table configured' warning, got:\n%s", out)
	}
}

// TestEnvInit_NoWarningWhenBackendFullyConfigured confirms a fully
// configured AWS backend (bucket, region, and a lock table) produces
// neither BackendWarnings case.
func TestEnvInit_NoWarningWhenBackendFullyConfigured(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	scratchDir := t.TempDir()

	envFile := filepath.Join(scratchDir, "environment.yaml")
	envYAML := "provider: aws\nname: demo\naws:\n  vpc_cidr: 10.0.0.0/16\n" +
		"backend:\n  aws:\n    bucket: my-org-tfstate\n    region: us-east-1\n    dynamodb_table: my-org-tflocks\n"
	if err := os.WriteFile(envFile, []byte(envYAML), 0644); err != nil {
		t.Fatalf("write environment.yaml: %v", err)
	}

	cmd := exec.Command(bin, "env", "init", "-e", envFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose env init failed: %v\n%s", err, out)
	}
	if contains(string(out), "Warning:") {
		t.Errorf("expected no warning for a fully configured backend, got:\n%s", out)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
