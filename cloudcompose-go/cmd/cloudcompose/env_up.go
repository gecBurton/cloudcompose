package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// envUpCmd runs `env init` (writing the environment's Terraform
// manifest) followed immediately by `terraform apply` on it -- the
// environment half of what a single bundled `up` command used to do.
// `compose up` is the app half: compile + apply against an
// already-`env up`'d directory.
//
// Terraform apply runs interactively (plan + y/n prompt) unless
// --auto-approve is set, and always runs even if the environment
// already exists (Terraform reports "No changes" then).
var envUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Initialize an environment (if needed) and apply it, in one command",
	Long: "Runs `cloud-compose env init`, then `terraform apply` on the " +
		"environment, in order.\n\n" +
		"Shows its plan and prompts for confirmation interactively by " +
		"default, and always runs even if the environment already exists " +
		"(Terraform itself reports \"No changes\" in that case).\n\n" +
		"--auto-approve skips the confirmation prompt, for non-interactive " +
		"callers (CI, scripts) that have already decided not to have a human " +
		"review the plan for this run -- off by default; a human should " +
		"normally see the plan before it applies to real infrastructure.",
	Run: runEnvUp,
}

func runEnvUp(cmd *cobra.Command, args []string) {
	envConfigFile, _ := cmd.Flags().GetString("env")
	autoApprove, _ := cmd.Flags().GetBool("auto-approve")

	envDir, err := initEnvironment(envConfigFile)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	fmt.Println()

	if err := terraformApply(envDir, autoApprove); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
}

func init() {
	envCmd.AddCommand(envUpCmd)

	envUpCmd.Flags().StringP("env", "e", "environment.yaml", "Path to the authored environment.yaml (see docs/authored-environment-config.md). Unlike --env on compose compile/ps/logs/down (an already-applied environment directory), this is the input file up itself applies.")
	envUpCmd.Flags().Bool("auto-approve", false, "Skip the terraform apply confirmation prompt, for non-interactive callers (CI, scripts). Off by default -- a human should normally review the plan first.")
}
