package main

import (
	"fmt"
	"os"

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

func init() {
	rootCmd.AddCommand(parseCmd)
	rootCmd.AddCommand(versionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
