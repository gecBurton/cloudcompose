package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gecburton/cloudcompose/internal/compiler"
	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/compiler/gcp"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cloudcompose",
	Short: "Docker Compose to Terraform compiler",
	Long:  "Compile Docker Compose files to Terraform JSON for AWS, Azure, and GCP",
}

// rootVersion is this binary's own version identifier, versioned
// independently of any other package metadata.
const rootVersion = "v0.2.0"

var parseCmd = &cobra.Command{
	Use:   "parse <file>",
	Short: "Parse a Docker Compose file and output JSON",
	Long:  "Parse a Docker Compose file using compose-go and output normalized JSON",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		output, err := compiler.ParseComposeJSON(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(output)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("cloudcompose " + rootVersion + " (Go)")
	},
}

var normalizeCmd = &cobra.Command{
	Use:   "normalize <file>",
	Short: "Parse and normalize a Docker Compose file to semantic model",
	Long:  "Parse a Docker Compose file and output the cloud-agnostic semantic model as JSON",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		// The compose file's parent directory name, not a hardcoded
		// default. Every resource name in every cloud is derived from
		// this, so silently defaulting it to a fixed string produced
		// identically-named, colliding resources for every project
		// compiled through this binary.
		projectName, err := cmd.Flags().GetString("project")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if projectName == "" {
			absPath, err := filepath.Abs(filePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving %q: %v\n", filePath, err)
				os.Exit(1)
			}
			projectName = filepath.Base(filepath.Dir(absPath))
		}

		composeApp, err := compiler.ParseCompose(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
			os.Exit(1)
		}

		semanticApp, err := compiler.Normalize(composeApp, projectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Normalize error: %v\n", err)
			os.Exit(1)
		}

		output, err := compiler.SemanticToJSON(semanticApp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "JSON error: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(output)
	},
}

var compileAWSCmd = &cobra.Command{
	Use:   "compile-aws <compose-file>",
	Short: "Compile a Docker Compose file to AWS Terraform JSON",
	Long: "Parse, normalize, infer AWS resources, and generate Terraform JSON " +
		"in one step (parse -> normalize -> infer -> generate), entirely in Go.",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		envDir, err := cmd.Flags().GetString("env")
		if err != nil || envDir == "" {
			fmt.Fprintln(os.Stderr, "Error: --env is required")
			os.Exit(1)
		}

		projectName, err := cmd.Flags().GetString("project")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if projectName == "" {
			absPath, err := filepath.Abs(filePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving %q: %v\n", filePath, err)
				os.Exit(1)
			}
			projectName = filepath.Base(filepath.Dir(absPath))
		}

		env, err := aws.LoadAwsEnvironment(envDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Environment error: %v\n", err)
			os.Exit(1)
		}

		composeApp, err := compiler.ParseCompose(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
			os.Exit(1)
		}

		semanticApp, err := compiler.Normalize(composeApp, projectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Normalize error: %v\n", err)
			os.Exit(1)
		}

		resources, err := aws.InferAWS(semanticApp, env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Inference error: %v\n", err)
			os.Exit(1)
		}

		output, err := aws.GenerateAWS(resources, env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Generate error: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(output)
	},
}

var compileAzureCmd = &cobra.Command{
	Use:   "compile-azure <compose-file>",
	Short: "Compile a Docker Compose file to Azure Terraform JSON",
	Long: "Parse, normalize, infer Azure resources, and generate Terraform JSON " +
		"in one step, mirroring compile-aws's design for the same reason: " +
		"avoiding the need to (de)serialize the Schedule interface type " +
		"through JSON as a separate input format.",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		envDir, err := cmd.Flags().GetString("env")
		if err != nil || envDir == "" {
			fmt.Fprintln(os.Stderr, "Error: --env is required")
			os.Exit(1)
		}

		projectName, err := cmd.Flags().GetString("project")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if projectName == "" {
			absPath, err := filepath.Abs(filePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving %q: %v\n", filePath, err)
				os.Exit(1)
			}
			projectName = filepath.Base(filepath.Dir(absPath))
		}

		env, err := azure.LoadAzureEnvironment(envDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Environment error: %v\n", err)
			os.Exit(1)
		}
		env.SubnetIndex, _ = cmd.Flags().GetInt("subnet-index")

		composeApp, err := compiler.ParseCompose(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
			os.Exit(1)
		}

		semanticApp, err := compiler.Normalize(composeApp, projectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Normalize error: %v\n", err)
			os.Exit(1)
		}

		resources, err := azure.InferAzure(semanticApp, env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Inference error: %v\n", err)
			os.Exit(1)
		}

		output, err := azure.GenerateAzure(resources, env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Generate error: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(output)
	},
}

var compileGcpCmd = &cobra.Command{
	Use:   "compile-gcp <compose-file>",
	Short: "Compile a Docker Compose file to GCP Terraform JSON",
	Long: "Parse, normalize, infer GCP resources, and generate Terraform JSON " +
		"in one step, mirroring compile-aws/compile-azure's design for the " +
		"same reason. Verified with deliberately lighter rigor than the " +
		"AWS/Azure paths: GCP has no golden examples or dedicated test suite " +
		"to check against.",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		envDir, err := cmd.Flags().GetString("env")
		if err != nil || envDir == "" {
			fmt.Fprintln(os.Stderr, "Error: --env is required")
			os.Exit(1)
		}

		projectName, err := cmd.Flags().GetString("project")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if projectName == "" {
			absPath, err := filepath.Abs(filePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving %q: %v\n", filePath, err)
				os.Exit(1)
			}
			projectName = filepath.Base(filepath.Dir(absPath))
		}

		env, err := gcp.LoadGcpEnvironment(envDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Environment error: %v\n", err)
			os.Exit(1)
		}

		composeApp, err := compiler.ParseCompose(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
			os.Exit(1)
		}

		semanticApp, err := compiler.Normalize(composeApp, projectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Normalize error: %v\n", err)
			os.Exit(1)
		}

		resources := gcp.InferGcp(semanticApp, env)

		output, err := gcp.GenerateGcp(resources, env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Generate error: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(output)
	},
}

func init() {
	rootCmd.AddCommand(parseCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(normalizeCmd)
	rootCmd.AddCommand(compileAWSCmd)
	rootCmd.AddCommand(compileAzureCmd)
	rootCmd.AddCommand(compileGcpCmd)

	normalizeCmd.Flags().StringP("project", "p", "", "Project name for resource naming (default: compose file's parent directory name)")
	compileAWSCmd.Flags().StringP("project", "p", "", "Project name for resource naming (default: compose file's parent directory name)")
	compileAWSCmd.Flags().StringP("env", "e", "", "Path to the AWS environment directory created by `cloudcompose init` (required)")
	compileAzureCmd.Flags().StringP("project", "p", "", "Project name for resource naming (default: compose file's parent directory name)")
	compileAzureCmd.Flags().StringP("env", "e", "", "Path to the Azure environment directory created by `cloudcompose init` (required)")
	compileAzureCmd.Flags().Int("subnet-index", 0, "This app's index into the environment's reserved apps_cidr range, unique per app sharing one environment (see docs/azure-app-isolation-design.md)")
	compileGcpCmd.Flags().StringP("project", "p", "", "Project name for resource naming (default: compose file's parent directory name)")
	compileGcpCmd.Flags().StringP("env", "e", "", "Path to the GCP environment directory created by `cloudcompose init` (required)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
