package main

import (
	"github.com/spf13/cobra"
)

// composeCmd is the parent for every subcommand that operates on a
// single app (deployed from a Docker Compose file) against an
// already-applied environment -- `compose up`/`down`/`ps`/`logs`,
// mirroring `docker compose`'s own noun-then-verb shape. `compile`
// stays top-level, not nested here: see compile.go.
var composeCmd = &cobra.Command{
	Use:   "compose",
	Short: "Deploy, tear down, and inspect a single app against an environment",
	Long: "Deploy, tear down, and inspect a single app (from a Docker Compose " +
		"file) against an already-applied environment.\n\n" +
		"`compose up` compiles the app's Terraform manifest and applies it. " +
		"`compose down` destroys a single app's own infrastructure, never the " +
		"shared environment it was deployed into. `compose ps`/`compose logs` " +
		"query the cloud directly for live status and log output.",
}

func init() {
	rootCmd.AddCommand(composeCmd)
}
