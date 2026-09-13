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
// half.
var composeUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Compile an app's Terraform manifest and apply it",
	Long: "Runs `cloud-compose compile`, then `terraform apply` on the app, " +
		"against an already-applied environment.\n\n" +
		"--env must point at the authored environment.yaml that produced the " +
		"environment to deploy into -- the same meaning --env has everywhere " +
		"else (env init/env up/compile/ps/logs/down). The environment must " +
		"already be applied (`cloud-compose env init` + `terraform apply`, or " +
		"`env up`) -- this never creates or modifies it; environment changes " +
		"are a deliberate act, never a side effect of deploying an app.\n\n" +
		"Shows its plan and prompts for confirmation interactively by " +
		"default. --auto-approve skips that prompt, for non-interactive " +
		"callers (CI, scripts) that have already decided not to have a human " +
		"review the plan for this run -- off by default; a human should " +
		"normally see the plan before it applies to real infrastructure.",
	Run: runComposeUp,
}

func runComposeUp(cmd *cobra.Command, args []string) {
	composeFileFlag, _ := cmd.Flags().GetString("file")
	envFile, _ := cmd.Flags().GetString("env")
	autoApprove, _ := cmd.Flags().GetBool("auto-approve")

	if envFile == "" {
		fmt.Fprintln(os.Stderr, "Error: --env is required")
		os.Exit(1)
	}

	composeFile, err := resolveComposeFile(composeFileFlag)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	appDir, err := compileApp(composeFile, envFile)
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

	composeUpCmd.Flags().StringP("env", "e", "", "Path to the authored environment.yaml that produced the environment to deploy into (must already be applied -- `cloud-compose env init`/`env up` first).")
	composeUpCmd.Flags().Bool("auto-approve", false, "Skip the terraform apply confirmation prompt, for non-interactive callers (CI, scripts). Off by default -- a human should normally review the plan first.")
}
