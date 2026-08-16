package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// upCmd orchestrates the full init -> apply -> compile -> apply flow in
// one command, but by default does not skip either `terraform apply`'s
// own plan review: each one runs as a normal, interactive `terraform
// apply` (Terraform's own y/n prompt included), not `-auto-approve`.
// This is a deliberate choice, not an oversight -- cloudcompose has
// never called `terraform apply` itself before (see docs/authored-
// environment-config.md's "No Cloud Compose Compiler-managed Terraform
// state or embedded terraform apply" non-goal, written before this
// command existed): `up` narrows that to "cloudcompose may invoke
// terraform apply, but a human must still see the plan and type yes
// before either apply proceeds" -- collapsing the four manual commands
// (init, terraform apply, compile, terraform apply) into one without
// removing the checkpoint that catches a bad plan (a wrong region, an
// unexpectedly large diff, a naming collision like an already-existing
// ALB) before it becomes real infrastructure.
//
// --auto-approve is the one deliberate escape hatch from that
// checkpoint, and is opt-in, off by default, and named to match
// `terraform apply`'s own flag rather than invent a new one -- it
// exists for non-interactive callers (CI pipelines, scripts) that have
// already decided not to have a human review the plan for this
// particular run, not as a default anyone reaches for on real
// infrastructure. See docs/authored-environment-config.md's non-goals
// section for how this fits alongside `down`'s identical flag.
//
// Every environment apply always runs, even if the environment already
// exists -- there is no "skip if already applied" detection. Terraform
// itself reports "No changes" and the prompt becomes a fast no-op in
// that case; this keeps the logic simple and means `up` behaves
// identically whether an environment is brand new or being reused by a
// second app.
//
// --env here means the authored environment.yaml file `up` itself
// applies -- not the already-applied environment *directory* --env
// means on compile/ps/logs/down. Both commands use the same flag name
// deliberately (so a user reaching for "how do I point this command at
// my environment" always tries the same flag), but the two really are
// different things: on those other commands, --env must already exist
// (created by a previous `init`/`up`); on `up`, this file is the input
// `up` itself turns into that directory via its own `init` step below.
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

	// 1. Initialize the environment (writes main.tf.json; a no-op
	// rewrite if the environment.yaml hasn't changed since last time).
	envDir, err := initEnvironment(envConfigFile)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	fmt.Println()

	// 2. Apply the environment. Always runs -- see upCmd's own doc
	// comment for why there's no "skip if already applied" check.
	if err := terraformApply(envDir, autoApprove); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	fmt.Println()

	// 3. Compile the app against the now-applied environment.
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

// terraformApply runs `terraform init` (idempotent, safe to re-run) and
// then a `terraform apply` in dir, with stdout/stderr connected
// directly to the calling terminal so Terraform's own plan output
// behaves exactly as it would if a human ran these two commands by hand
// in that directory. When autoApprove is false (the default), stdin is
// also connected and no -auto-approve is passed, so Terraform's own y/n
// confirmation prompt behaves the same way too. When autoApprove is
// true, -auto-approve is passed and stdin is left unconnected -- there
// is no prompt to answer, which matters for non-interactive callers
// (CI) whose stdin may not be a terminal at all.
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

// terraformInit runs `terraform init` (idempotent, safe to re-run) in
// dir, connected to the calling terminal's stdout/stderr. Shared by
// terraformApply (up.go) and terraformDestroy (down.go) -- both need an
// initialized working directory before running their own Terraform
// subcommand, and neither needs stdin connected for `init` itself.
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
