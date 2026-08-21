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
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
	"github.com/spf13/cobra"
)

// mainCmd parses, normalizes, compiles, and writes Terraform JSON to
// disk, plus copying any Docker build contexts alongside the manifest.
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

	// --env and --demo are mutually exclusive: exactly one is required.
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

// compileApp loads the environment (from envDir, or synthesizes one
// from demoCloud), parses and normalizes composeFile, infers and
// generates Terraform JSON, and writes it (plus any Docker build
// contexts) to <dir of composeFile>/app-<environment name>-<project
// name>, returning that output directory. Exactly one of
// envDir/demoCloud must be non-empty.
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
			"run `cloud-compose env init` to set up a real one.")
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
	// not a fixed "terraform" directory, so different environments or
	// projects compiled from the same compose file don't overwrite each
	// other's output.
	envName, err := environmentName(env)
	if err != nil {
		return "", err
	}
	outputDir := filepath.Join(filepath.Dir(absCompose), "app-"+envName+"-"+projectName)

	// --subnet-index only means something on Azure; ignored on AWS/GCP.
	if azureEnv, ok := env.(*models.AzureEnvironment); ok {
		azureEnv.SubnetIndex = subnetIndex
	}

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
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}
	outputFile := filepath.Join(outputDir, "main.tf.json")
	if err := os.WriteFile(outputFile, []byte(tfJSON), 0644); err != nil {
		return "", err
	}

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

// requireAwsOrAzure rejects env if it isn't an AWS or Azure
// environment, naming cmdName (e.g. "ps", "logs") in the resulting
// error.
func requireAwsOrAzure(cmdName string, env any) error {
	switch env.(type) {
	case *models.AwsEnvironment, *models.AzureEnvironment:
		return nil
	default:
		target, _ := environmentTarget(env)
		return fmt.Errorf("`cloud-compose compose %s` does not support %s environments yet", cmdName, target)
	}
}

// environmentName reports the environment's own name (the `name:`
// authored in environment.yaml, or "demo" for --demo).
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

// environmentBackend reports the environment's Backend config (nil if
// none configured).
func environmentBackend(env any) (*models.BackendConfig, error) {
	switch e := env.(type) {
	case *models.AwsEnvironment:
		return e.Backend, nil
	case *models.AzureEnvironment:
		return e.Backend, nil
	case *models.GcpEnvironment:
		return e.Backend, nil
	default:
		return nil, fmt.Errorf("unsupported environment type %T", env)
	}
}

// appDir reports the app-<environment name>-<project name> output
// directory compileApp writes to, without compiling anything.
// projectName must already be resolved by the caller.
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

// resolveProjectName returns projectName unchanged if given explicitly
// via -p/--project; otherwise it defaults to composeFile's own
// containing directory name. Validates the resolved name via
// shared.ValidateBackendName, since it's used to build backend state
// keys and an unsanitized name could collide with another app's key.
func resolveProjectName(composeFile, projectName string) (string, error) {
	if projectName == "" {
		absCompose, err := filepath.Abs(composeFile)
		if err != nil {
			return "", err
		}
		projectName = filepath.Base(filepath.Dir(absCompose))
	}
	if err := shared.ValidateBackendName("project", projectName); err != nil {
		return "", err
	}
	return projectName, nil
}

// demoEnvironment builds a synthetic environment for --demo.
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

// compileTerraform dispatches to the correct Go compile-<cloud>
// pipeline based on the concrete environment type: parse, normalize,
// infer, and generate.
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
		return aws.GenerateAWS(resources, e, projectName)
	case *models.AzureEnvironment:
		resources, err := azure.InferAzure(semanticApp, e)
		if err != nil {
			return "", err
		}
		return azure.GenerateAzure(resources, e, projectName)
	case *models.GcpEnvironment:
		resources := gcp.InferGcp(semanticApp, e)
		return gcp.GenerateGcp(resources, e, projectName)
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

	mainCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloud-compose env init` (terraform apply must have run there already)")
	mainCmd.Flags().StringP("demo", "d", "", "Generate placeholder Terraform for evaluation, with no real environment: one of aws, azure, gcp. Mutually exclusive with --env.")
	mainCmd.Flags().StringP("project", "p", "", "Name of the project (defaults to the directory name)")
	mainCmd.Flags().Bool("explain", false, "Report every inference the compiler makes, and write nothing")
	mainCmd.Flags().BoolP("version", "v", false, "Show the version and exit")
	mainCmd.Flags().Int("subnet-index", 0, "Azure only: this app's index into the environment's reserved apps_cidr range, unique per app sharing one environment (see docs/azure-app-isolation-design.md). Ignored on AWS/GCP.")
}

// cloudcomposeVersion returns a short identifying string for the CLI.
func cloudcomposeVersion() string {
	return "cloud-compose " + rootVersion
}
