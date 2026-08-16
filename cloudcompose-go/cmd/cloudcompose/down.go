package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// downCmd runs `terraform destroy` against a single already-compiled
// app's own Terraform directory (app-<environment name>-<project
// name>), the inverse of `cloudcompose compile`. It deliberately never
// touches the shared environment `cloudcompose init` created --
// destroying a VPC/ALB/ECS cluster out from under every other app still
// using it is not something a single app's teardown should ever do
// implicitly. Tearing down the environment itself is a separate,
// explicit `terraform destroy` run by hand in its own env-<name>
// directory (see docs/authored-environment-config.md), matching the
// same "environment is shared, apps are not" split `cloudcompose
// init`/`compile` already draw.
//
// Like up.go's terraformApply, this runs an ordinary `terraform
// destroy` by default -- Terraform's own plan and y/n confirmation
// prompt included, no -auto-approve -- for the same reason: a human
// should see exactly what's about to be deleted before it happens.
// --auto-approve is the same opt-in, off-by-default escape hatch up.go
// offers, for non-interactive callers (CI, scripts); see that flag's
// own doc comment on upCmd for the full reasoning, which applies
// identically here.
var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy a deployed app's infrastructure (not the shared environment)",
	Long: "Runs `terraform destroy` in the app's own Terraform directory " +
		"(app-<environment name>-<project name>, written by a previous " +
		"`cloudcompose compile`), the inverse of `compile`.\n\n" +
		"This only ever destroys the app -- it never touches the shared " +
		"environment `cloudcompose init` created, since other apps may " +
		"still depend on it. Destroy an environment itself the same way " +
		"you created it: `terraform destroy` by hand in its own env-<name> " +
		"directory.\n\n" +
		"Shows its plan and prompts for confirmation interactively by " +
		"default, like every other command that runs Terraform. " +
		"--auto-approve skips that prompt, for non-interactive callers " +
		"(CI, scripts) that have already decided not to have a human " +
		"review the plan for this run -- off by default.",
	Run: runDown,
}

func runDown(cmd *cobra.Command, args []string) {
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

	// Must resolve to exactly the same project name `compile` used to
	// produce this app's output directory in the first place -- see
	// appDir's own doc comment. If this app was compiled with an
	// explicit --project, that same value must be passed here too;
	// there is no way to recover it automatically, since it is not
	// itself recorded anywhere `down` can read it back from.
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
		fmt.Fprintf(os.Stderr, "Error: %s does not exist -- has `cloudcompose compile` run for this app and environment yet? "+
			"If compile was given an explicit --project, pass the same one here.\n", dir)
		os.Exit(1)
	}

	if err := terraformDestroy(dir, autoApprove); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
}

// terraformDestroy runs `terraform init` (idempotent, safe to re-run)
// and then a `terraform destroy` in dir, mirroring up.go's
// terraformApply exactly: interactive with stdin connected and no
// -auto-approve when autoApprove is false (the default), or
// -auto-approve passed with stdin left unconnected when true.
func terraformDestroy(dir string, autoApprove bool) error {
	fmt.Printf("Running terraform destroy in %s\n", dir)

	if err := terraformInit(dir); err != nil {
		return err
	}

	args := []string{"destroy"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}
	destroyCmd := exec.Command("terraform", args...)
	destroyCmd.Dir = dir
	if !autoApprove {
		destroyCmd.Stdin = os.Stdin
	}
	destroyCmd.Stdout = os.Stdout
	destroyCmd.Stderr = os.Stderr
	if err := destroyCmd.Run(); err != nil {
		return fmt.Errorf("terraform destroy in %s: %w", dir, err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(downCmd)

	downCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloudcompose init` (terraform apply must have run there already)")
	downCmd.Flags().StringP("project", "p", "", "Name of the project this app was compiled with (defaults to the compose file's own directory name, same as `compile`). Must match whatever `compile` used to produce the app's output directory.")
	downCmd.Flags().Bool("auto-approve", false, "Skip the terraform destroy confirmation prompt, for non-interactive callers (CI, scripts). Off by default -- a human should normally review the plan first.")
}
