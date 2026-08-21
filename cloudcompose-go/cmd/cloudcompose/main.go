package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cloudcompose",
	Short: "Docker Compose to Terraform compiler",
	Long:  "Compile Docker Compose files to Terraform JSON for AWS, Azure, and GCP",
}

// rootVersion is this binary's version, overridden at build time via
// -ldflags "-X main.rootVersion=vX.Y.Z" by the release workflow.
var rootVersion = "v0.2.0-dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("cloudcompose " + rootVersion + " (Go)")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)

	// Persistent so it works before or after the subcommand (e.g. both
	// `cloudcompose -f x.yml compile` and `cloudcompose compile -f
	// x.yml`), matching `docker compose`'s own flag positioning.
	rootCmd.PersistentFlags().StringP("file", "f", "", "Path to the Docker Compose file (defaults to compose.yaml/compose.yml/docker-compose.yaml/docker-compose.yml in the current directory, like `docker compose`)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
