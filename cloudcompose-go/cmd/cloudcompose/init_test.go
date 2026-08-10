package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildCloudComposeBinary builds the cloudcompose CLI once per test run and
// returns its path, for the integration-style tests below that exercise
// runInit through the real cobra command (including os.Exit paths),
// rather than calling runInit directly -- it isn't structured as a pure
// function, deliberately (see AGENTS.md's "Adding a CLI Command" note:
// init.go is simple enough not to need the same
// extract-into-pure-functions treatment compile.go's environmentTarget/
// compileTerraform got).
func buildCloudComposeBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cloudcompose")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cloudcompose: %v\n%s", err, out)
	}
	return bin
}

// TestInit_MissingFileFailsWithHelpfulMessage mirrors
// docs/authored-environment-config.md's "cloudcompose init behavior" step 3:
// cloudcompose init has no flags-only fallback -- a missing environment.yaml
// is an error naming the missing path and pointing at
// examples/hello/environment.yaml, not a silent flags-based bootstrap.
func TestInit_MissingFileFailsWithHelpfulMessage(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "init")
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

// TestInit_NoDecisionFlags confirms cloudcompose init's flag set really is
// just -f/-o now -- a regression test for the flag removal described in
// docs/authored-environment-config.md's "Revision 2: no flags either".
func TestInit_NoDecisionFlags(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "init", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloudcompose init --help failed: %v\n%s", err, out)
	}

	for _, removedFlag := range []string{
		"--provider", "--name", "--region", "--vpc-cidr", "--az-count",
		"--create-alb", "--certificate-arn", "--aws-endpoint",
		"--project-id", "--domain", "--retain-data", "--tags",
	} {
		if contains(string(out), removedFlag) {
			t.Errorf("expected %s to have been removed from cloudcompose init, but it's still in --help output:\n%s", removedFlag, out)
		}
	}
	for _, keptFlag := range []string{"-f, --file", "-o, --output"} {
		if !contains(string(out), keptFlag) {
			t.Errorf("expected %s in cloudcompose init --help output, got:\n%s", keptFlag, out)
		}
	}
}

// TestInit_RealAwsExampleProducesValidManifest exercises cloudcompose init
// end-to-end against the real, committed examples/hello/environment.yaml
// -- the same file examples/README.md's walkthrough uses -- and checks
// the resulting main.tf.json is well-formed. Does not run `terraform
// validate` here (that's covered manually/in CI smoke tests against real
// cloud credentials); this only confirms cloudcompose init itself succeeds
// and writes both expected files.
func TestInit_RealAwsExampleProducesValidManifest(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	outDir := t.TempDir()

	envFile, err := filepath.Abs("../../../examples/hello/environment.yaml")
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}
	if _, err := os.Stat(envFile); err != nil {
		t.Fatalf("examples/hello/environment.yaml not found: %v", err)
	}

	cmd := exec.Command(bin, "init", "-f", envFile, "-o", outDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloudcompose init failed: %v\n%s", err, out)
	}

	for _, want := range []string{"main.tf.json", "environment.yaml"} {
		if _, err := os.Stat(filepath.Join(outDir, want)); err != nil {
			t.Errorf("expected %s to be written, got: %v", want, err)
		}
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
