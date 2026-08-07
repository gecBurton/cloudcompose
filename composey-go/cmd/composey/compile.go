package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gecburton/composey/internal/compiler"
	"github.com/gecburton/composey/internal/compiler/aws"
	"github.com/gecburton/composey/internal/compiler/azure"
	"github.com/gecburton/composey/internal/compiler/gcp"
	"github.com/gecburton/composey/internal/models"
	"github.com/spf13/cobra"
)

// mainCmd is the primary compile command, mirroring composey/cli.py's
// `main` Typer command: parse, normalize, (optionally explain), compile,
// and write Terraform JSON to disk, plus copying any Docker build
// contexts alongside the manifest.
var mainCmd = &cobra.Command{
	Use:   "main",
	Short: "Compile a Docker Compose file into deterministic Terraform JSON",
	Long:  "Compile a Docker Compose file into deterministic Terraform JSON.",
	Run:   runMain,
}

func runMain(cmd *cobra.Command, args []string) {
	if showVersion, _ := cmd.Flags().GetBool("version"); showVersion {
		fmt.Println(composeyVersion())
		return
	}

	composeFile, _ := cmd.Flags().GetString("file")
	envFile, _ := cmd.Flags().GetString("env")
	projectName, _ := cmd.Flags().GetString("project")
	outputDir, _ := cmd.Flags().GetString("out")
	explainOnly, _ := cmd.Flags().GetBool("explain")

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

	// Explaining needs no environment: every inference reported here is
	// made before the target is consulted.
	if explainOnly {
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
		fmt.Println(compiler.StripMarkup(compiler.Render(compiler.Explain(nil, semantic))))
		return
	}

	if envFile == "" {
		fmt.Fprintln(os.Stderr, "Error: --env is required to compile")
		os.Exit(1)
	}

	// 1. Load environment.
	fmt.Printf("Loading environment: %s\n", envFile)
	env, err := compiler.LoadEnvironment(envFile)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	target, err := environmentTarget(env)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	// 2. Compile.
	fmt.Printf("Compiling: %s -> %s (%s)\n", composeFile, projectName, target)

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

	// Report anything the compiler could not decide.
	decisions := compiler.Explain(nil, semantic)
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
	// warnings step). Redundant work, not a correctness issue -- matches
	// cli.py's own documented tradeoff exactly, for the same reason
	// (threading a pre-parsed Application through would require the Go
	// backends to accept semantic JSON input, which the Schedule
	// interface type makes non-trivial; see hybrid.py in the Python tree
	// this was ported from, and compiler/infer_*.go's own one-step CLI
	// subcommand design in this one).
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	// 3. Write output.
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	outputFile := filepath.Join(outputDir, "main.tf.json")
	if err := os.WriteFile(outputFile, []byte(tfJSON), 0644); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	// Copy any Docker build contexts next to the manifest.
	composeDir := filepath.Dir(absCompose)
	if err := copyDockerBuildContexts(tfJSON, composeDir, outputDir); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	fmt.Printf("Success! Terraform manifest written to %s\n", outputFile)
}

// environmentTarget reports the cloud target name for an environment
// value returned by LoadEnvironment, mirroring env.target on Python's
// BaseEnvironment.
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

// compileTerraform dispatches to the correct Go compile-<cloud> pipeline
// based on the concrete environment type, mirroring
// composey/compiler/__init__.py's compile_to_terraform: parse, normalize,
// infer, and generate, all again from the compose file (see the note at
// this function's call site for why that repeats work already done for
// the warnings step above, and why that's not fixed here).
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
// file to next to the written manifest, mirroring cli.py's own
// shutil.copytree loop over resource.docker_image.
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
	if os.Getenv("COMPOSEY_DEBUG") != "" {
		panic(err)
	}
}

// copyDir recursively copies a directory tree, mirroring
// shutil.copytree(src, dst, dirs_exist_ok=True): destination directories
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

	mainCmd.Flags().StringP("file", "f", "compose.yml", "Path to the Docker Compose file")
	mainCmd.Flags().StringP("env", "e", "", "Path to the Environment configuration YAML")
	mainCmd.Flags().StringP("project", "p", "", "Name of the project (defaults to the directory name)")
	mainCmd.Flags().StringP("out", "o", "terraform", "Directory to write the generated Terraform JSON")
	mainCmd.Flags().Bool("explain", false, "Report every inference the compiler makes, and write nothing")
	mainCmd.Flags().BoolP("version", "v", false, "Show the version and exit")
}

// composeyVersion mirrors cli.py's _get_version()/version_callback: a
// short identifying string, not intended to track Python's own
// importlib.metadata-sourced version number byte-for-byte (the two
// binaries are versioned independently once this becomes the standalone
// CLI).
func composeyVersion() string {
	return "composey " + rootVersion
}
