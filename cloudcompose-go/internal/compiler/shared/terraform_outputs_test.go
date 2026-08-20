package shared

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeTerraformOutputsFixture mirrors
// internal/compiler/environment_test.go's own helper of the same name:
// a scratch directory with a single named output (no providers, no
// resources), applied with `terraform apply` so TerraformOutputs/
// OptionalTerraformOutputs have real state to read. No network access
// needed: zero providers means `terraform init` completes offline.
func writeTerraformOutputsFixture(t *testing.T, outputName, valueHCL string) string {
	t.Helper()
	dir := t.TempDir()

	mainTF := fmt.Sprintf(`output %q {
  value = %s
}
`, outputName, valueHCL)
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

// TestTerraformOutputs_ReadsNamedOutput is a basic smoke test that
// TerraformOutputs decodes a real, applied Terraform output correctly
// -- every other test in this file builds on this working.
func TestTerraformOutputs_ReadsNamedOutput(t *testing.T) {
	t.Parallel()
	dir := writeTerraformOutputsFixture(t, "environment", `{ name = "prod" }`)

	value, err := TerraformOutputs(dir, "environment")
	if err != nil {
		t.Fatalf("TerraformOutputs failed: %v", err)
	}
	if value["name"] != "prod" {
		t.Errorf("name = %v, want prod", value["name"])
	}
}

// TestTerraformOutputs_MissingOutputIsAnError confirms TerraformOutputs
// itself still treats a missing output as a hard error -- only
// OptionalTerraformOutputs swallows this case (see its own doc
// comment).
func TestTerraformOutputs_MissingOutputIsAnError(t *testing.T) {
	t.Parallel()
	dir := writeTerraformOutputsFixture(t, "environment", `{ name = "prod" }`)

	_, err := TerraformOutputs(dir, "backend")
	if err == nil {
		t.Fatal("expected an error for a missing output")
	}
}

// TestOptionalTerraformOutputs_MissingOutputReturnsNilNil is the
// behavior docs/multi-user-state.md's "no backend configured" default
// depends on: an environment generated without backend: configured has
// no `backend` output at all (see
// internal/compiler/{aws,azure,gcp}/environment_generator.go), and
// OptionalTerraformOutputs must treat that as "no backend", not an
// error LoadAwsEnvironment/LoadAzureEnvironment/LoadGcpEnvironment would
// otherwise have to propagate.
func TestOptionalTerraformOutputs_MissingOutputReturnsNilNil(t *testing.T) {
	t.Parallel()
	dir := writeTerraformOutputsFixture(t, "environment", `{ name = "prod" }`)

	value, err := OptionalTerraformOutputs(dir, "backend")
	if err != nil {
		t.Fatalf("expected no error for a missing optional output, got %v", err)
	}
	if value != nil {
		t.Errorf("expected nil value, got %v", value)
	}
}

// TestOptionalTerraformOutputs_PresentOutputBehavesLikeTerraformOutputs
// confirms OptionalTerraformOutputs doesn't change behavior at all when
// the output actually exists -- the "optional" part only kicks in for
// the missing case.
func TestOptionalTerraformOutputs_PresentOutputBehavesLikeTerraformOutputs(t *testing.T) {
	t.Parallel()
	dir := writeTerraformOutputsFixture(t, "backend", `{ provider = "aws" }`)

	value, err := OptionalTerraformOutputs(dir, "backend")
	if err != nil {
		t.Fatalf("OptionalTerraformOutputs failed: %v", err)
	}
	if value["provider"] != "aws" {
		t.Errorf("provider = %v, want aws", value["provider"])
	}
}

// TestOptionalTerraformOutputs_OtherErrorsStillPropagate confirms a
// genuinely different failure (here: the output exists but isn't an
// object, the same "wrong shape" error TerraformOutputs itself returns)
// is not swallowed the way a missing output is -- only the specific
// "no such output" case is optional.
func TestOptionalTerraformOutputs_OtherErrorsStillPropagate(t *testing.T) {
	t.Parallel()
	dir := writeTerraformOutputsFixture(t, "backend", `"not-an-object"`)

	_, err := OptionalTerraformOutputs(dir, "backend")
	if err == nil {
		t.Fatal("expected an error when the output exists but isn't an object")
	}
}
