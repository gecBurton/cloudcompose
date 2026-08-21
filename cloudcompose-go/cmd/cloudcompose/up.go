package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// upCmd runs init -> apply -> compile -> apply in one command. Both
// terraform apply steps run interactively (plan + y/n prompt) unless
// --auto-approve is set, and the environment apply always runs even if
// the environment already exists (Terraform reports "No changes" then).
var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Initialize an environment (if needed) and deploy an app into it, in one command",
	Long: "Runs `cloudcompose init`, `terraform apply` on the environment, " +
		"`cloudcompose compile`, and `terraform apply` on the app, in order.\n\n" +
		"Both `terraform apply` steps show their plan and prompt for " +
		"confirmation interactively by default, and always run the " +
		"environment's apply step even if the environment already exists " +
		"(Terraform itself reports \"No changes\" in that case).\n\n" +
		"--auto-approve skips both confirmation prompts, for non-interactive " +
		"callers (CI, scripts) that have already decided not to have a human " +
		"review the plan for this run -- off by default; a human should " +
		"normally see the plan before it applies to real infrastructure.",
	Run: runUp,
}

func runUp(cmd *cobra.Command, args []string) {
	composeFileFlag, _ := cmd.Flags().GetString("file")
	envConfigFile, _ := cmd.Flags().GetString("env")
	projectName, _ := cmd.Flags().GetString("project")
	subnetIndex, _ := cmd.Flags().GetInt("subnet-index")
	autoApprove, _ := cmd.Flags().GetBool("auto-approve")

	composeFile, err := resolveComposeFile(composeFileFlag)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

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
	fmt.Println()

	appDir, err := compileApp(composeFile, envDir, "", projectName, subnetIndex)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	fmt.Printf("Success! Terraform manifest written to %s\n", filepath.Join(appDir, "main.tf.json"))
	fmt.Println()

	// 4. Apply the app.
	if err := terraformApply(appDir, autoApprove); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
}

// terraformApply runs `terraform init` then `terraform apply` in dir,
// with stdin/stdout/stderr connected to the terminal. If autoApprove is
// false, stdin is connected and no -auto-approve is passed, so
// Terraform's own confirmation prompt behaves normally.
func terraformApply(dir string, autoApprove bool) error {
	fmt.Printf("Running terraform in %s\n", dir)

	if err := terraformInit(dir); err != nil {
		return err
	}

	args := []string{"apply"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}
	applyCmd := exec.Command("terraform", args...)
	applyCmd.Dir = dir
	if !autoApprove {
		applyCmd.Stdin = os.Stdin
	}
	applyCmd.Stdout = os.Stdout
	applyCmd.Stderr = os.Stderr
	if err := applyCmd.Run(); err != nil {
		return fmt.Errorf("terraform apply in %s: %w", dir, err)
	}
	return nil
}

// terraformInit runs `terraform init` in dir.
func terraformInit(dir string) error {
	initCmd := exec.Command("terraform", "init", "-input=false")
	initCmd.Dir = dir
	initCmd.Stdout = os.Stdout
	initCmd.Stderr = os.Stderr
	if err := initCmd.Run(); err != nil {
		return fmt.Errorf("terraform init in %s: %w", dir, err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(upCmd)

	upCmd.Flags().StringP("env", "e", "environment.yaml", "Path to the authored environment.yaml (see docs/authored-environment-config.md). Unlike --env on compile/ps/logs/down (an already-applied environment directory), this is the input file up itself applies.")
	upCmd.Flags().StringP("project", "p", "", "Name of the project (defaults to the directory name)")
	upCmd.Flags().Int("subnet-index", 0, "Azure only: this app's index into the environment's reserved apps_cidr range, unique per app sharing one environment (see docs/azure-app-isolation-design.md). Ignored on AWS/GCP.")
	upCmd.Flags().Bool("auto-approve", false, "Skip both terraform apply confirmation prompts, for non-interactive callers (CI, scripts). Off by default -- a human should normally review the plan first.")
}
