package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

func TestEnvironmentTarget(t *testing.T) {
	t.Parallel()
	aws := models.NewAwsEnvironment()
	azure := models.NewAzureEnvironment()
	gcp := models.NewGcpEnvironment()

	cases := []struct {
		name string
		env  any
		want string
	}{
		{"aws", &aws, "aws"},
		{"azure", &azure, "azure"},
		{"gcp", &gcp, "gcp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := environmentTarget(tc.env)
			if err != nil {
				t.Fatalf("environmentTarget failed: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnvironmentTarget_RejectsUnsupportedType(t *testing.T) {
	t.Parallel()
	_, err := environmentTarget("not an environment")
	if err == nil {
		t.Fatal("expected an error for an unsupported type")
	}
}

// TestCompileTerraform_DispatchesToAWS is a light integration check that
// compileTerraform's type-switch dispatch actually reaches the AWS
// pipeline and produces valid Terraform JSON for the real hello example.
func TestCompileTerraform_DispatchesToAWS(t *testing.T) {
	t.Parallel()
	env := models.NewAwsEnvironment()
	env.Name = "prod"
	env.VpcID = "vpc-1"
	env.PublicSubnets = []string{"s1"}
	env.PrivateSubnets = []string{"s2"}
	env.EcsClusterArn = "arn:aws:ecs:us-east-1:1:cluster/c"

	out, err := compileTerraform("../../../examples/hello/compose.yml", &env, "hello")
	if err != nil {
		t.Fatalf("compileTerraform failed: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestCompileTerraform_RejectsUnsupportedType(t *testing.T) {
	t.Parallel()
	_, err := compileTerraform("../../../examples/hello/compose.yml", "not an environment", "hello")
	if err == nil {
		t.Fatal("expected an error for an unsupported environment type")
	}
}

// TestCopyDir_RecursivelyCopiesFilesAndSubdirectories mirrors what
// copyDockerBuildContexts relies on: shutil.copytree(dirs_exist_ok=True)
// semantics -- nested files and directories all copied, destination
// created as needed.
func TestCopyDir_RecursivelyCopiesFilesAndSubdirectories(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "nested", "dest")

	if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0644); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(src, "sub")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "nested.txt"), []byte("nested"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir failed: %v", err)
	}

	topContent, err := os.ReadFile(filepath.Join(dst, "top.txt"))
	if err != nil || string(topContent) != "top" {
		t.Errorf("top.txt = %q, err %v, want 'top'", topContent, err)
	}
	nestedContent, err := os.ReadFile(filepath.Join(dst, "sub", "nested.txt"))
	if err != nil || string(nestedContent) != "nested" {
		t.Errorf("sub/nested.txt = %q, err %v, want 'nested'", nestedContent, err)
	}
}

// TestCopyDockerBuildContexts_CopiesReferencedContexts mirrors cli.py's
// own loop over resource.docker_image, using the real doctor example
// (which has a build_context).
func TestCopyDockerBuildContexts_CopiesReferencedContexts(t *testing.T) {
	t.Parallel()
	env := models.NewAwsEnvironment()
	env.Name = "prod"
	env.VpcID = "vpc-1"
	env.PublicSubnets = []string{"s1"}
	env.PrivateSubnets = []string{"s2"}
	env.EcsClusterArn = "arn:aws:ecs:us-east-1:1:cluster/c"

	tfJSON, err := compileTerraform("../../../examples/doctor/compose.yml", &env, "doctor")
	if err != nil {
		t.Fatalf("compileTerraform failed: %v", err)
	}

	composeDir, err := filepath.Abs("../../../examples/doctor")
	if err != nil {
		t.Fatal(err)
	}
	outputDir := t.TempDir()

	if err := copyDockerBuildContexts(tfJSON, composeDir, outputDir); err != nil {
		t.Fatalf("copyDockerBuildContexts failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outputDir, "app")); err != nil {
		t.Errorf("expected the 'app' build context to be copied, got: %v", err)
	}
}

// TestCopyDockerBuildContexts_NoDockerImagesIsANoOp checks that a
// manifest with no docker_image resources doesn't error.
func TestCopyDockerBuildContexts_NoDockerImagesIsANoOp(t *testing.T) {
	t.Parallel()
	outputDir := t.TempDir()
	if err := copyDockerBuildContexts(`{"resource": {}}`, outputDir, outputDir); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
