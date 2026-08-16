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

	// -f/--file is persistent (works before or after the subcommand,
	// e.g. both `cloudcompose -f x.yml compile` and `cloudcompose
	// compile -f x.yml`), exactly matching real `docker compose`'s own
	// flag positioning. This is deliberate, not incidental: real `docker
	// compose logs` also has a `-f`, but it means --follow there, a
	// *local* flag on `logs` that shadows this persistent one -- the two
	// only coexist because --file is persistent and --follow is local,
	// the same relationship this flag needs here for `cloudcompose logs`
	// to be able to add its own -f/--follow later without a shorthand
	// collision. `cloudcompose init` already defines its own local -f
	// for a conceptually different flag (environment.yaml, not a
	// compose file) and continues to shadow this one, which is correct:
	// `init` doesn't take a compose file at all.
	rootCmd.PersistentFlags().StringP("file", "f", "", "Path to the Docker Compose file (defaults to compose.yaml/compose.yml/docker-compose.yaml/docker-compose.yml in the current directory, like `docker compose`)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
