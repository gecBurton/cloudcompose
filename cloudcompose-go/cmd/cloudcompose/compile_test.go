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

	cmd := exec.Command(bin, "compile", "-f", composeFile, "-d", "aws", "-p", "hello")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose compile --demo aws failed: %v\n%s", err, out)
	}
	if !contains(string(out), "DEMO MODE") {
		t.Errorf("expected a demo-mode warning, got:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(composeDir, "app-demo-hello", "main.tf.json")); statErr != nil {
		t.Errorf("expected main.tf.json to be written, got: %v", statErr)
	}
}

// TestMain_FileFlagWorksBeforeOrAfterSubcommand confirms -f/--file is a
// persistent root flag, not a local one repeated per-subcommand: it
// must work both before the subcommand (`cloud-compose -f x.yml
// compile`) and after it (`cloud-compose compile -f x.yml`), exactly
// like real `docker compose`'s own -f positioning. This split is
// deliberate, not cosmetic -- see main.go's own doc comment: it's what
// lets `cloud-compose logs` define a *local* -f/--follow later (like
// real `docker compose logs -f`) without colliding with this one.
func TestMain_FileFlagWorksBeforeOrAfterSubcommand(t *testing.T) {
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

	beforeSubcommand := exec.Command(bin, "-f", composeFile, "compile", "-d", "aws")
	out, err := beforeSubcommand.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose -f %s compile failed: %v\n%s", composeFile, err, out)
	}
	// compile defaults --project to composeDir's own basename, so the
	// output directory is app-demo-<composeDir's basename> here rather
	// than a fixed name.
	appDirName := "app-demo-" + filepath.Base(composeDir)
	if _, statErr := os.Stat(filepath.Join(composeDir, appDirName, "main.tf.json")); statErr != nil {
		t.Errorf("expected main.tf.json from -f before the subcommand, got: %v", statErr)
	}
	if err := os.RemoveAll(filepath.Join(composeDir, appDirName)); err != nil {
		t.Fatalf("cleanup %s: %v", appDirName, err)
	}

	afterSubcommand := exec.Command(bin, "compile", "-f", composeFile, "-d", "aws")
	out, err = afterSubcommand.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose compile -f %s failed: %v\n%s", composeFile, err, out)
	}
	if _, statErr := os.Stat(filepath.Join(composeDir, appDirName, "main.tf.json")); statErr != nil {
		t.Errorf("expected main.tf.json from -f after the subcommand, got: %v", statErr)
	}
}

// TestMain_FileFlagIsOptionalWhenComposeFileExistsInCwd confirms -f/
// --file is only required when there's genuine ambiguity, matching
// `docker compose`'s own behavior: with a compose.yml present in the
// working directory and no -f given at all, `cloud-compose compile`
// should still find and use it (see shared.FindComposeFile).
func TestMain_FileFlagIsOptionalWhenComposeFileExistsInCwd(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	composeDir := t.TempDir()

	composeSrc, err := os.ReadFile("../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("read example compose.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(composeDir, "compose.yml"), composeSrc, 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	cmd := exec.Command(bin, "compile", "-d", "aws", "-p", "hello")
	cmd.Dir = composeDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose compile with no -f failed: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(composeDir, "app-demo-hello", "main.tf.json")); statErr != nil {
		t.Errorf("expected main.tf.json to be written, got: %v", statErr)
	}
}

// TestMain_FileFlagMissingWithNoComposeFileInCwd confirms the absence of
// any compose file in the working directory (and no -f) fails with a
// clear message naming every filename that was tried, rather than a
// generic "file not found" for a literal "compose.yml".
func TestMain_FileFlagMissingWithNoComposeFileInCwd(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	emptyDir := t.TempDir()

	cmd := exec.Command(bin, "compile", "-d", "aws")
	cmd.Dir = emptyDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit with no compose file in cwd, got success:\n%s", out)
	}
	if !contains(string(out), "no compose file found") {
		t.Errorf("expected a 'no compose file found' message, got:\n%s", out)
	}
}

// TestMain_DifferentProjectsAgainstSameEnvironmentDoNotCollide is a
// regression test for a real bug found in review: compileApp's output
// directory used to be app-<environment name> alone, so two different
// --project values compiled against the same compose.yml/environment
// pair silently overwrote each other's main.tf.json on disk, even
// though every actual Terraform resource they produce is genuinely
// different (every resource name is env.Name-app.Name-..., so a
// different --project really is a different deployment, not a
// re-compile of the same one). The fix folds --project into the output
// directory (app-<environment name>-<project name>); this test compiles
// the same compose file against the same environment under two
// different --project values and asserts both outputs exist
// side-by-side with different content, neither overwriting the other.
func TestMain_DifferentProjectsAgainstSameEnvironmentDoNotCollide(t *testing.T) {
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

	for _, project := range []string{"appA", "appB"} {
		cmd := exec.Command(bin, "compile", "-f", composeFile, "-d", "aws", "-p", project)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cloud-compose compile -p %s failed: %v\n%s", project, err, out)
		}
	}

	appADir := filepath.Join(composeDir, "app-demo-appA", "main.tf.json")
	appBDir := filepath.Join(composeDir, "app-demo-appB", "main.tf.json")
	appAContent, err := os.ReadFile(appADir)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", appADir, err)
	}
	appBContent, err := os.ReadFile(appBDir)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", appBDir, err)
	}
	if string(appAContent) == string(appBContent) {
		t.Error("expected appA's and appB's manifests to differ (different project names produce different resource names), got identical content")
	}
	if !contains(string(appAContent), "appA") {
		t.Error("expected appA's manifest to reference its own project name")
	}
	if !contains(string(appBContent), "appB") {
		t.Error("expected appB's manifest to reference its own project name")
	}
}

// TestMain_ExplainReportsDroppedPortsFromRealComposeModel is a
// regression test for a real bug found in review: both call sites of
// compiler.Explain in runMain/compileApp used to pass nil for the raw
// compose model even though a real one was already parsed and sitting
// in scope, silently disabling every Decision (like portDecisions'
// "ports N are not exposed" warning below) that depends on comparing
// the raw compose.yml against the normalized semantic model rather than
// the normalized model alone. A service that publishes two ports (only
// the first of which cloudcompose actually uses) is the simplest way to
// reproduce it: this warning must appear in --explain output, and must
// be counted into a normal `compile`'s own warning count.
func TestMain_ExplainReportsDroppedPortsFromRealComposeModel(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)
	composeDir := t.TempDir()

	composeFile := filepath.Join(composeDir, "compose.yml")
	composeContent := "services:\n  backend:\n    image: nginx\n    ports:\n      - \"3000:3000\"\n      - \"3001:3001\"\n"
	if err := os.WriteFile(composeFile, []byte(composeContent), 0644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	explainOut, err := exec.Command(bin, "compile", "-f", composeFile, "--explain").CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose compile --explain failed: %v\n%s", err, explainOut)
	}
	if !contains(string(explainOut), "3001") || !contains(string(explainOut), "not exposed") {
		t.Errorf("expected --explain to report ports 3001 are not exposed, got:\n%s", explainOut)
	}

	compileOut, err := exec.Command(bin, "compile", "-f", composeFile, "-d", "aws", "-p", "portstest").CombinedOutput()
	if err != nil {
		t.Fatalf("cloud-compose compile failed: %v\n%s", err, compileOut)
	}
	if !contains(string(compileOut), "3001") {
		t.Errorf("expected a normal compile's own warning summary to also report the dropped port, got:\n%s", compileOut)
	}
}

// TestResolveProjectName_RejectsExplicitProjectContainingSlash is the
// regression test for the backend-state-key collision
// shared.ValidateBackendName exists to prevent (see its own doc
// comment): a project name is also the input to
// shared.BackendKeyForApp, so it must be rejected here before it can
// ever reach that function -- mirroring
// initconfig.TestLoad_RejectsNameContainingSlash's identical check on
// an environment's own `name:`.
func TestResolveProjectName_RejectsExplicitProjectContainingSlash(t *testing.T) {
	t.Parallel()
	_, err := resolveProjectName("compose.yml", "prod/apps")
	if err == nil {
		t.Fatal("expected an error when --project contains '/'")
	}
}

// TestResolveProjectName_DefaultedNameIsStillValidated confirms the
// same check applies even when the project name is defaulted from the
// compose file's own containing directory name, not just an explicit
// --project -- a directory name is still an untrusted string as far as
// backend key construction is concerned.
func TestResolveProjectName_DefaultedNameIsStillValidated(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "prod-apps")
	// Deliberately does not create dir/compose.yml on disk --
	// resolveProjectName never stats the file itself, only
	// filepath.Abs/Dir/Base on the path string, so this doesn't need to
	// exist. "prod-apps" contains no "/", so this confirms a safe
	// defaulted name is accepted; the explicit-project case above
	// exercises the actual slash-rejection regression.
	if _, err := resolveProjectName(filepath.Join(dir, "compose.yml"), ""); err != nil {
		t.Errorf("expected a safe defaulted project name to be accepted, got: %v", err)
	}
}

// TestResolveProjectName_AcceptsSafeNames confirms ordinary project
// names (explicit or defaulted) are unaffected by the new validation.
func TestResolveProjectName_AcceptsSafeNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"checkout-api", "web_api", "hello"} {
		got, err := resolveProjectName("compose.yml", name)
		if err != nil {
			t.Errorf("resolveProjectName(%q) failed: %v", name, err)
		}
		if got != name {
			t.Errorf("resolveProjectName(%q) = %q, want unchanged", name, got)
		}
	}
}
