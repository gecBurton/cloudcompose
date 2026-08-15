package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
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

func TestDemoEnvironment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cloud string
		want  string
	}{
		{"aws", "aws"},
		{"azure", "azure"},
		{"gcp", "gcp"},
	}
	for _, tc := range cases {
		t.Run(tc.cloud, func(t *testing.T) {
			env, err := demoEnvironment(tc.cloud)
			if err != nil {
				t.Fatalf("demoEnvironment(%q) failed: %v", tc.cloud, err)
			}
			got, err := environmentTarget(env)
			if err != nil {
				t.Fatalf("environmentTarget failed: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDemoEnvironment_RejectsUnknownCloud(t *testing.T) {
	t.Parallel()
	_, err := demoEnvironment("nonsense")
	if err == nil {
		t.Fatal("expected an error for an unrecognised cloud name")
	}
}

// TestDemoEnvironment_CompilesRealExample is a light integration check
// that every demo environment (not just AWS's, per
// TestCompileTerraform_DispatchesToAWS above) reaches its full
// infer/generate pipeline and produces valid Terraform JSON, the same
// way --demo is actually used from the CLI.
func TestDemoEnvironment_CompilesRealExample(t *testing.T) {
	t.Parallel()
	for _, cloud := range []string{"aws", "azure", "gcp"} {
		cloud := cloud
		t.Run(cloud, func(t *testing.T) {
			t.Parallel()
			env, err := demoEnvironment(cloud)
			if err != nil {
				t.Fatalf("demoEnvironment(%q) failed: %v", cloud, err)
			}
			out, err := compileTerraform("../../../examples/hello/compose.yml", env, "hello")
			if err != nil {
				t.Fatalf("compileTerraform failed: %v", err)
			}
			if out == "" {
				t.Error("expected non-empty output")
			}
		})
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

// TestCopyDockerBuildContexts_CopiesReferencedContexts uses the real
// doctor example (which has a build_context).
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

// TestMain_RequiresEnvOrDemo confirms --env and --demo really are the
// only two ways to supply an environment: neither given is an error, not
// a silent default (see runMain's own "one way to configure, not two"
// comment, mirroring init.go's).
func TestMain_RequiresEnvOrDemo(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "compile", "-f", "../../../examples/hello/compose.yml")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit when neither --env nor --demo is given, got success:\n%s", out)
	}
	if !contains(string(out), "--env or --demo is required") {
		t.Errorf("expected the error to name both flags, got:\n%s", out)
	}
}

// TestMain_RejectsBothEnvAndDemo confirms --env and --demo are mutually
// exclusive, not silently resolved by preferring one.
func TestMain_RejectsBothEnvAndDemo(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "compile",
		"-f", "../../../examples/hello/compose.yml",
		"-e", "../../../examples/hello",
		"-d", "aws")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit when both --env and --demo are given, got success:\n%s", out)
	}
	if !contains(string(out), "mutually exclusive") {
		t.Errorf("expected the error to say the two flags are mutually exclusive, got:\n%s", out)
	}
}

// TestMain_DemoRejectsUnknownCloud confirms --demo validates its argument
// against the known cloud set rather than passing an unrecognised value
// through to LoadEnvironment-shaped code.
func TestMain_DemoRejectsUnknownCloud(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "compile", "-f", "../../../examples/hello/compose.yml", "-d", "nonsense")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit for an unrecognised --demo cloud, got success:\n%s", out)
	}
	if !contains(string(out), "aws, azure, gcp") {
		t.Errorf("expected the error to list the valid clouds, got:\n%s", out)
	}
}

// TestMain_DemoWritesTerraformWithNoEnvironment is the real end-to-end
// path: --demo alone, no --env, no environment directory anywhere,
// should still produce a compilable main.tf.json plus the demo-mode
// warning banner on stderr. Output now has no --out override -- it's
// always written to <dir of --file>/terraform -- so this copies
// compose.yml into a scratch directory rather than writing into the
// real examples/hello directory.
func TestMain_DemoWritesTerraformWithNoEnvironment(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	composeDir := t.TempDir()

	composeSrc, err := os.ReadFile("../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("read example compose.yml: %v", err)
	}
	composeFile := filepath.Join(composeDir, "compose.yml")
	if err := os.WriteFile(composeFile, composeSrc, 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	cmd := exec.Command(bin, "compile", "-f", composeFile, "-d", "aws")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloudcompose compile --demo aws failed: %v\n%s", err, out)
	}
	if !contains(string(out), "DEMO MODE") {
		t.Errorf("expected a demo-mode warning, got:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(composeDir, "app-demo", "main.tf.json")); statErr != nil {
		t.Errorf("expected main.tf.json to be written, got: %v", statErr)
	}
}
