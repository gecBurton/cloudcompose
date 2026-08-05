package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gecburton/composey/internal/compiler"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "composey",
	Short: "Docker Compose to Terraform compiler",
	Long:  "Compile Docker Compose files to Terraform JSON for AWS, Azure, and GCP",
}

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
		fmt.Println("composey v0.2.0 (Go)")
	},
}

var normalizeCmd = &cobra.Command{
	Use:   "normalize <file>",
	Short: "Parse and normalize a Docker Compose file to semantic model",
	Long:  "Parse a Docker Compose file and output the cloud-agnostic semantic model as JSON",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		// Matches the Python CLI's fallback (cli.py): the compose file's
		// parent directory name, not a hardcoded default. Every resource
		// name in every cloud is derived from this, so silently defaulting
		// it to a fixed string produced identically-named, colliding
		// resources for every project compiled through this binary.
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

func init() {
	rootCmd.AddCommand(parseCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(normalizeCmd)

	normalizeCmd.Flags().StringP("project", "p", "", "Project name for resource naming (default: compose file's parent directory name)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
