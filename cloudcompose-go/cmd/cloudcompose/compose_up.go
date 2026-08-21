package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// composeUpCmd compiles a single app's Terraform manifest against an
// already-applied environment and applies it -- the app half of what a
// single bundled `up` command used to do. `env up` is the environment
// half: init + apply on a shared environment.
//
// --env here means an already-applied environment *directory* (created
// by a previous `cloud-compose env init`/`env up`), the same meaning it
// has on compile/ps/logs/down, not an authored environment.yaml file.
//
// Terraform apply runs interactively (plan + y/n prompt) unless
// --auto-approve is set.
var composeUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Compile an app's Terraform manifest and apply it",
	Long: "Runs `cloud-compose compile`, then `terraform apply` on the app, " +
		"against an already-applied environment.\n\n" +
		"--env must point at an environment directory created by a previous " +
		"`cloud-compose env init`/`env up` (terraform apply must have already " +
		"run there) -- the same meaning --env has on compile/ps/logs/down, not " +
		"an authored environment.yaml file.\n\n" +
		"Shows its plan and prompts for confirmation interactively by " +
		"default. --auto-approve skips that prompt, for non-interactive " +
		"callers (CI, scripts) that have already decided not to have a human " +
		"review the plan for this run -- off by default; a human should " +
		"normally see the plan before it applies to real infrastructure.",
	Run: runComposeUp,
}

func runComposeUp(cmd *cobra.Command, args []string) {
	composeFileFlag, _ := cmd.Flags().GetString("file")
	envDir, _ := cmd.Flags().GetString("env")
	projectName, _ := cmd.Flags().GetString("project")
	subnetIndex, _ := cmd.Flags().GetInt("subnet-index")
	autoApprove, _ := cmd.Flags().GetBool("auto-approve")

	if envDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --env is required")
		os.Exit(1)
	}

	composeFile, err := resolveComposeFile(composeFileFlag)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	appDir, err := compileApp(composeFile, envDir, "", projectName, subnetIndex)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	fmt.Printf("Success! Terraform manifest written to %s\n", filepath.Join(appDir, "main.tf.json"))
	fmt.Println()

	if err := terraformApply(appDir, autoApprove); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
}

func init() {
	composeCmd.AddCommand(composeUpCmd)

	composeUpCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloud-compose env init`/`env up` (terraform apply must have run there already)")
	composeUpCmd.Flags().StringP("project", "p", "", "Name of the project (defaults to the directory name)")
	composeUpCmd.Flags().Int("subnet-index", 0, "Azure only: this app's index into the environment's reserved apps_cidr range, unique per app sharing one environment (see docs/azure-app-isolation-design.md). Ignored on AWS/GCP.")
	composeUpCmd.Flags().Bool("auto-approve", false, "Skip the terraform apply confirmation prompt, for non-interactive callers (CI, scripts). Off by default -- a human should normally review the plan first.")
}
