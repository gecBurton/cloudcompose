package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// composeDownCmd runs `terraform destroy` against a single
// already-compiled app's own Terraform directory (app-<environment
// name>-<project name>), the inverse of `cloud-compose compile`. It
// never touches the shared environment `cloud-compose env init`
// created -- tearing that down is `cloud-compose env down`, a
// separate, explicit command that runs its own dependent-app safety
// check first (see env_down.go).
var composeDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy a deployed app's infrastructure (not the shared environment)",
	Long: "Runs `terraform destroy` in the app's own Terraform directory " +
		"(app-<environment name>-<project name>, written by a previous " +
		"`cloud-compose compile`), the inverse of `compile`.\n\n" +
		"This only ever destroys the app -- it never touches the shared " +
		"environment `cloud-compose env init` created, since other apps may " +
		"still depend on it. Destroy an environment itself with " +
		"`cloud-compose env down`.\n\n" +
		"--env is the authored environment.yaml that produced the " +
		"environment this app was compiled against, the same meaning --env " +
		"has everywhere else.\n\n" +
		"Shows its plan and prompts for confirmation interactively by " +
		"default, like every other command that runs Terraform. " +
		"--auto-approve skips that prompt, for non-interactive callers " +
		"(CI, scripts) that have already decided not to have a human " +
		"review the plan for this run -- off by default.",
	Run: runComposeDown,
}

func runComposeDown(cmd *cobra.Command, args []string) {
	composeFileFlag, _ := cmd.Flags().GetString("file")
	envFile, _ := cmd.Flags().GetString("env")
	autoApprove, _ := cmd.Flags().GetBool("auto-approve")

	if envFile == "" {
		fmt.Fprintln(os.Stderr, "Error: --env is required")
		os.Exit(1)
	}

	composeFile, err := resolveComposeFile(composeFileFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	dir, err := appDir(composeFile, envFile)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %s does not exist -- has `cloud-compose compile` run for this app and environment yet?\n", dir)
		os.Exit(1)
	}

	if err := terraformDestroy(dir, autoApprove); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(composeDownCmd)

	composeDownCmd.Flags().StringP("env", "e", "", "Path to the authored environment.yaml that produced the environment this app was compiled against (must already be applied).")
	composeDownCmd.Flags().Bool("auto-approve", false, "Skip the terraform destroy confirmation prompt, for non-interactive callers (CI, scripts). Off by default -- a human should normally review the plan first.")
}
