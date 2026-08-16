package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gecburton/cloudcompose/internal/compiler"
	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/compiler/gcp"
	"github.com/gecburton/cloudcompose/internal/models"
	"github.com/spf13/cobra"
)

// mainCmd is the primary compile command: parse, normalize, (optionally
// explain), compile, and write Terraform JSON to disk, plus copying any
// Docker build contexts alongside the manifest.
var mainCmd = &cobra.Command{
	Use:   "compile",
	Short: "Compile a Docker Compose file into deterministic Terraform JSON",
	Long:  "Compile a Docker Compose file into deterministic Terraform JSON.",
	Run:   runMain,
}

func runMain(cmd *cobra.Command, args []string) {
	if showVersion, _ := cmd.Flags().GetBool("version"); showVersion {
		fmt.Println(cloudcomposeVersion())
		return
	}

	composeFileFlag, _ := cmd.Flags().GetString("file")
	envDir, _ := cmd.Flags().GetString("env")
	demoCloud, _ := cmd.Flags().GetString("demo")
	projectName, _ := cmd.Flags().GetString("project")
	explainOnly, _ := cmd.Flags().GetBool("explain")
	subnetIndex, _ := cmd.Flags().GetInt("subnet-index")

	composeFile, err := resolveComposeFile(composeFileFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Explaining needs no environment: every inference reported here is
	// made before the target is consulted.
	if explainOnly {
		absCompose, err := filepath.Abs(composeFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if _, err := os.Stat(absCompose); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s does not exist or is not readable\n", composeFile)
			os.Exit(1)
		}
		if projectName == "" {
			projectName = filepath.Base(filepath.Dir(absCompose))
		}
		composeApp, err := compiler.ParseCompose(composeFile)
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		semantic, err := compiler.Normalize(composeApp, projectName)
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		fmt.Println(compiler.StripMarkup(compiler.Render(compiler.Explain(composeApp, semantic))))
		return
	}

	// --env and --demo are mutually exclusive, deliberate ways to supply
	// an environment: one reads real Terraform-managed facts, the other
	// synthesizes plausible-looking placeholder ones. Requiring exactly
	// one (not defaulting either way when both are absent, and not
	// silently preferring one when both are given) matches init.go's own
	// "one way to configure, not two" reasoning -- this is the same
	// choice applied to a second decision point.
	if envDir == "" && demoCloud == "" {
		fmt.Fprintln(os.Stderr, "Error: --env or --demo is required to compile")
		os.Exit(1)
	}
	if envDir != "" && demoCloud != "" {
		fmt.Fprintln(os.Stderr, "Error: --env and --demo are mutually exclusive")
		os.Exit(1)
	}

	outputDir, err := compileApp(composeFile, envDir, demoCloud, projectName, subnetIndex)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	fmt.Printf("Success! Terraform manifest written to %s\n", filepath.Join(outputDir, "main.tf.json"))
}

// compileApp does everything `cloudcompose compile` does -- load the
// environment (from envDir, or synthesize one from demoCloud), parse and
// normalize composeFile, infer and generate Terraform JSON, and write it
// (plus copying any Docker build contexts) to <dir of composeFile>/
// app-<environment name> -- and returns that output directory. Exactly
// one of envDir/demoCloud must be non-empty; the caller (runMain, or
// cloudcompose up in up.go) is responsible for enforcing that and for
// printing the --demo warning banner, since up.go's own messaging
// differs slightly from runMain's.
func compileApp(composeFile, envDir, demoCloud, projectName string, subnetIndex int) (string, error) {
	absCompose, err := filepath.Abs(composeFile)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(absCompose); err != nil {
		return "", fmt.Errorf("%s does not exist or is not readable", composeFile)
	}

	projectName, err = resolveProjectName(composeFile, projectName)
	if err != nil {
		return "", err
	}

	var env any
	if demoCloud != "" {
		env, err = demoEnvironment(demoCloud)
		if err != nil {
			return "", err
		}
		fmt.Fprintln(os.Stderr, "DEMO MODE: using placeholder resource IDs, not a real environment. "+
			"The generated Terraform is for evaluation only and is not deployable as-is — "+
			"run `cloudcompose init` to set up a real one.")
	} else {
		fmt.Printf("Loading environment: %s\n", envDir)
		env, err = compiler.LoadEnvironment(envDir)
		if err != nil {
			return "", err
		}
	}
	target, err := environmentTarget(env)
	if err != nil {
		return "", err
	}

	// Output lands in <dir of -f>/app-<environment name>-<project name>,
	// not a fixed "terraform" directory: the same compose.yml compiled
	// against two different environments (e.g. dev vs prod) must not
	// overwrite each other's output, and two different --project values
	// compiled against the *same* environment must not either -- every
	// resource this compile produces is named env.Name-app.Name-... (see
	// e.g. aws/infer.go's getName closure), so two different project
	// names really do produce two different, non-interchangeable sets of
	// resources; the output directory naming must not imply they're the
	// same deployment. Named to pair with init's own env-<name> output
	// directory -- app-<name>-<project> is this app's slice of that
	// environment's name plus its own identity within it.
	envName, err := environmentName(env)
	if err != nil {
		return "", err
	}
	outputDir := filepath.Join(filepath.Dir(absCompose), "app-"+envName+"-"+projectName)

	// --subnet-index only means something on Azure -- see
	// AzureEnvironment.SubnetIndex's own doc comment for why it's a
	// flag (a per-app decision) rather than an environment.yaml field
	// (a per-environment one), and docs/azure-app-isolation-design.md
	// for the full design. Ignored on AWS/GCP, which need no per-app
	// subnet: their own isolation model doesn't have this gap.
	if azureEnv, ok := env.(*models.AzureEnvironment); ok {
		azureEnv.SubnetIndex = subnetIndex
	}

	// 2. Compile.
	fmt.Printf("Compiling: %s -> %s (%s)\n", composeFile, projectName, target)

	composeApp, err := compiler.ParseCompose(composeFile)
	if err != nil {
		return "", err
	}
	semantic, err := compiler.Normalize(composeApp, projectName)
	if err != nil {
		return "", err
	}

	// Report anything the compiler could not decide.
	decisions := compiler.Explain(composeApp, semantic)
	warningCount := 0
	for _, d := range decisions {
		if d.Source == compiler.SourceWarning {
			warningCount++
			fmt.Printf("warning %s: %s\n", d.Subject, d.Decision)
		}
	}
	if warningCount > 0 {
		fmt.Printf("%d warning(s) — run with --explain for detail\n", warningCount)
	}

	tfJSON, err := compileTerraform(composeFile, env, projectName)
	// Note: for AWS/Azure/GCP environments this re-parses and
	// re-normalizes the compose file a second time (the Go compile-<cloud>
	// subcommand does parse+normalize+infer+generate in one call,
	// separately from the `semantic` object already built above for the
	// warnings step). Redundant work, not a correctness issue -- threading
	// a pre-parsed Application through would require the Go backends to
	// accept semantic JSON input, which the Schedule interface type makes
	// non-trivial, so each cloud's own one-step CLI subcommand design
	// (compiler/infer_*.go) just re-does the parse/normalize step instead.
	if err != nil {
		return "", err
	}

	// 3. Write output.
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}
	outputFile := filepath.Join(outputDir, "main.tf.json")
	if err := os.WriteFile(outputFile, []byte(tfJSON), 0644); err != nil {
		return "", err
	}

	// Copy any Docker build contexts next to the manifest.
	composeDir := filepath.Dir(absCompose)
	if err := copyDockerBuildContexts(tfJSON, composeDir, outputDir); err != nil {
		return "", err
	}

	return outputDir, nil
}

// environmentTarget reports the cloud target name for an environment
// value returned by LoadEnvironment.
func environmentTarget(env any) (string, error) {
	switch env.(type) {
	case *models.AwsEnvironment:
		return "aws", nil
	case *models.AzureEnvironment:
		return "azure", nil
	case *models.GcpEnvironment:
		return "gcp", nil
	default:
		return "", fmt.Errorf("unsupported environment type %T", env)
	}
}

// requireAwsOrAzure rejects env if it isn't an AWS or Azure environment,
// naming cmdName (e.g. "ps", "logs") in the resulting error the same way
// each command's own type-switch default case already did. Every one of
// ps/logs/down's own real work (aws.FetchStatus/FetchLogs,
// azure.FetchStatus/FetchLogs) only exists for AWS/Azure; GCP has no
// equivalent yet, and unlike an actually-unsupported target,
// LoadEnvironment itself has nothing to reject for GCP -- it's a real,
// supported cloudcompose target elsewhere (compile/init), just not for
// these three commands specifically. Callers use this to fail
// immediately after LoadEnvironment succeeds, before doing any further
// work (parsing/normalizing compose.yml, or in ps.go's original bug,
// even reaching the type switch that would have rejected it anyway) --
// found during a review as unnecessary wasted work on a GCP environment
// specifically, not a correctness bug (the command still failed
// correctly, just later than it needed to).
func requireAwsOrAzure(cmdName string, env any) error {
	switch env.(type) {
	case *models.AwsEnvironment, *models.AzureEnvironment:
		return nil
	default:
		target, _ := environmentTarget(env)
		return fmt.Errorf("`cloudcompose %s` does not support %s environments yet", cmdName, target)
	}
}

// environmentName reports the environment's own name (the same `name:`
// authored in environment.yaml, or "demo" for --demo's synthetic
// environments), used to build compile's own output directory
// (app-<name>) -- see that call site's own comment for why this needs
// to vary per environment rather than being a fixed "terraform"
// directory.
func environmentName(env any) (string, error) {
	switch e := env.(type) {
	case *models.AwsEnvironment:
		return e.Name, nil
	case *models.AzureEnvironment:
		return e.Name, nil
	case *models.GcpEnvironment:
		return e.Name, nil
	default:
		return "", fmt.Errorf("unsupported environment type %T", env)
	}
}

// appDir reports the same app-<environment name>-<project name> output
// directory compileApp writes to for composeFile compiled against the
// environment in envDir under projectName, without compiling anything
// -- used by `cloudcompose down` (down.go) to find an already-compiled
// app's directory to run `terraform destroy` in. projectName must be
// resolved (defaulted from the compose file's own directory name, same
// as compileApp does) by the caller before calling this -- see down.go's
// own use of resolveProjectName. Deliberately takes no --demo option:
// demo environments never produce real infrastructure, so there is
// nothing for `down` to destroy against one.
func appDir(composeFile, envDir, projectName string) (string, error) {
	absCompose, err := filepath.Abs(composeFile)
	if err != nil {
		return "", err
	}
	env, err := compiler.LoadEnvironment(envDir)
	if err != nil {
		return "", err
	}
	envName, err := environmentName(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(absCompose), "app-"+envName+"-"+projectName), nil
}

// resolveProjectName returns projectName unchanged if the caller gave
// one explicitly via -p/--project; otherwise it defaults to
// composeFile's own containing directory's name, exactly matching
// compileApp's own default so a caller resolving the project name ahead
// of compileApp (as down.go must, to reconstruct the same output
// directory without compiling anything) gets the identical value.
func resolveProjectName(composeFile, projectName string) (string, error) {
	if projectName != "" {
		return projectName, nil
	}
	absCompose, err := filepath.Abs(composeFile)
	if err != nil {
		return "", err
	}
	return filepath.Base(filepath.Dir(absCompose)), nil
}

// demoEnvironment builds a synthetic environment for --demo, one of the
// same NewDemo*Environment values every golden fixture proves compiles
// cleanly through the real infer/generate pipeline (see each
// NewDemo*Environment's own doc comment in internal/models/environment.go).
// Returns a pointer, matching what LoadEnvironment returns for a real
// environment, since compileTerraform's own type switch expects one.
func demoEnvironment(cloud string) (any, error) {
	switch cloud {
	case "aws":
		env := models.NewDemoAwsEnvironment()
		return &env, nil
	case "azure":
		env := models.NewDemoAzureEnvironment()
		return &env, nil
	case "gcp":
		env := models.NewDemoGcpEnvironment()
		return &env, nil
	default:
		return nil, fmt.Errorf("--demo must be one of aws, azure, gcp (got %q)", cloud)
	}
}

// compileTerraform dispatches to the correct Go compile-<cloud> pipeline
// based on the concrete environment type: parse, normalize, infer, and
// generate, all again from the compose file (see the note at this
// function's call site for why that repeats work already done for the
// warnings step above, and why that's not fixed here).
func compileTerraform(composeFile string, env any, projectName string) (string, error) {
	composeApp, err := compiler.ParseCompose(composeFile)
	if err != nil {
		return "", err
	}
	semanticApp, err := compiler.Normalize(composeApp, projectName)
	if err != nil {
		return "", err
	}

	switch e := env.(type) {
	case *models.AwsEnvironment:
		resources, err := aws.InferAWS(semanticApp, e)
		if err != nil {
			return "", err
		}
		return aws.GenerateAWS(resources, e)
	case *models.AzureEnvironment:
		resources, err := azure.InferAzure(semanticApp, e)
		if err != nil {
			return "", err
		}
		return azure.GenerateAzure(resources, e)
	case *models.GcpEnvironment:
		resources := gcp.InferGcp(semanticApp, e)
		return gcp.GenerateGcp(resources, e)
	default:
		return "", fmt.Errorf("unsupported environment type %T", env)
	}
}

// copyDockerBuildContexts copies every build context a docker_image
// resource in the compiled manifest references, from next to the compose
// file to next to the written manifest.
func copyDockerBuildContexts(tfJSON, composeDir, outputDir string) error {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(tfJSON), &parsed); err != nil {
		return fmt.Errorf("parse generated Terraform JSON: %w", err)
	}

	resource, _ := parsed["resource"].(map[string]any)
	dockerImages, _ := resource["docker_image"].(map[string]any)

	for _, imageAny := range dockerImages {
		image, ok := imageAny.(map[string]any)
		if !ok {
			continue
		}
		build, ok := image["build"].(map[string]any)
		if !ok {
			continue
		}
		context, _ := build["context"].(string)
		if context == "" {
			continue
		}

		src := filepath.Join(composeDir, context)
		dst := filepath.Join(outputDir, context)

		info, err := os.Stat(src)
		if err != nil || !info.IsDir() {
			continue
		}
		if err := copyDir(src, dst); err != nil {
			return fmt.Errorf("copy build context %s: %w", context, err)
		}
		fmt.Printf("Copied build context: %s\n", context)
	}
	return nil
}

func printUnexpectedError(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	if os.Getenv("CLOUDCOMPOSE_DEBUG") != "" {
		panic(err)
	}
}

// copyDir recursively copies a directory tree: destination directories
// are created as needed, and an existing destination is merged into
// rather than rejected.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func init() {
	rootCmd.AddCommand(mainCmd)

	mainCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloudcompose init` (terraform apply must have run there already)")
	mainCmd.Flags().StringP("demo", "d", "", "Generate placeholder Terraform for evaluation, with no real environment: one of aws, azure, gcp. Mutually exclusive with --env.")
	mainCmd.Flags().StringP("project", "p", "", "Name of the project (defaults to the directory name)")
	mainCmd.Flags().Bool("explain", false, "Report every inference the compiler makes, and write nothing")
	mainCmd.Flags().BoolP("version", "v", false, "Show the version and exit")
	mainCmd.Flags().Int("subnet-index", 0, "Azure only: this app's index into the environment's reserved apps_cidr range, unique per app sharing one environment (see docs/azure-app-isolation-design.md). Ignored on AWS/GCP.")
}

// cloudcomposeVersion returns a short identifying string for the CLI.
func cloudcomposeVersion() string {
	return "cloudcompose " + rootVersion
}
