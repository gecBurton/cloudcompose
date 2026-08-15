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

// rootVersion is this binary's own version identifier, versioned
// independently of any other package metadata. Overridden at build time
// via -ldflags "-X main.rootVersion=vX.Y.Z" by the release workflow
// (see .goreleaser.yaml); a plain `go build` with no ldflags keeps this
// fallback so local/dev builds still report something sensible.
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
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
