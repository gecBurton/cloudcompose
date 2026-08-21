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
		"Shows its plan and prompts for confirmation interactively by " +
		"default, like every other command that runs Terraform. " +
		"--auto-approve skips that prompt, for non-interactive callers " +
		"(CI, scripts) that have already decided not to have a human " +
		"review the plan for this run -- off by default.",
	Run: runComposeDown,
}

func runComposeDown(cmd *cobra.Command, args []string) {
	composeFileFlag, _ := cmd.Flags().GetString("file")
	envDir, _ := cmd.Flags().GetString("env")
	projectFlag, _ := cmd.Flags().GetString("project")
	autoApprove, _ := cmd.Flags().GetBool("auto-approve")

	if envDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --env is required")
		os.Exit(1)
	}

	composeFile, err := resolveComposeFile(composeFileFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Must resolve to the same project name `compile` used to produce
	// this app's output directory. If compile was given an explicit
	// --project, that same value must be passed here too.
	projectName, err := resolveProjectName(composeFile, projectFlag)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	dir, err := appDir(composeFile, envDir, projectName)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %s does not exist -- has `cloud-compose compile` run for this app and environment yet? "+
			"If compile was given an explicit --project, pass the same one here.\n", dir)
		os.Exit(1)
	}

	if err := terraformDestroy(dir, autoApprove); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
}

func init() {
	composeCmd.AddCommand(composeDownCmd)

	composeDownCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloud-compose env init` (terraform apply must have run there already)")
	composeDownCmd.Flags().StringP("project", "p", "", "Name of the project this app was compiled with (defaults to the compose file's own directory name, same as `compile`). Must match whatever `compile` used to produce the app's output directory.")
	composeDownCmd.Flags().Bool("auto-approve", false, "Skip the terraform destroy confirmation prompt, for non-interactive callers (CI, scripts). Off by default -- a human should normally review the plan first.")
}
