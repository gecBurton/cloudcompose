package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler"
	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/compiler/gcp"
	"github.com/spf13/cobra"
)

// envDestroyCmd runs `terraform destroy` against a shared environment
// directory (env-<name>, written by a previous `cloudcompose init`).
// Unlike down.go's app-level teardown, this first checks whether any
// app still depends on the environment (via each cloud's own
// ListDependentApps) and refuses by default if any are found.
var envDestroyCmd = &cobra.Command{
	Use:   "env-destroy",
	Short: "Destroy a shared environment's infrastructure (refuses if apps still depend on it)",
	Long: "Runs `terraform destroy` in the environment's own Terraform directory " +
		"(env-<name>, written by a previous `cloudcompose init`).\n\n" +
		"Unlike `cloudcompose down` (which only ever destroys a single app), " +
		"this destroys the shared environment itself -- so it first checks " +
		"whether any app still depends on it (every app compiled against a " +
		"backend-configured environment registers its own state under that " +
		"environment's own backend, see docs/multi-user-state.md) and refuses " +
		"by default if any are found, listing their project names and " +
		"suggesting `cloudcompose down` for each first.\n\n" +
		"Without a configured backend, this check has nothing to list against " +
		"and is skipped with a warning -- the same as if it found no dependent " +
		"apps, but without the guarantee that none exist.\n\n" +
		"--force skips the dependent-app check entirely, for cases where the " +
		"check itself can't run (e.g. a permissions error) or the operator has " +
		"already confirmed by other means that no apps depend on this " +
		"environment. Off by default.\n\n" +
		"Shows its plan and prompts for confirmation interactively by default, " +
		"like every other command that runs Terraform. --auto-approve skips " +
		"that prompt, for non-interactive callers (CI, scripts).",
	Run: runEnvDestroy,
}

func runEnvDestroy(cmd *cobra.Command, args []string) {
	envDir, _ := cmd.Flags().GetString("env")
	force, _ := cmd.Flags().GetBool("force")
	autoApprove, _ := cmd.Flags().GetBool("auto-approve")

	if envDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --env is required")
		os.Exit(1)
	}
	if _, statErr := os.Stat(envDir); statErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %s does not exist -- has `cloudcompose init` run for this environment yet?\n", envDir)
		os.Exit(1)
	}

	env, err := compiler.LoadEnvironment(envDir)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	if !force {
		if err := checkNoDependentApps(env); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Fprintln(os.Stderr, "\nRun `cloudcompose down` for each app listed above first, or pass --force to skip this check.")
			os.Exit(1)
		}
	}

	if err := terraformDestroy(envDir, autoApprove); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
}

// checkNoDependentApps returns an error listing every app that still
// depends on env (via each cloud's own ListDependentApps), or nil if
// none do. If env has no backend configured, or the list call itself
// fails, this prints a warning and returns nil rather than blocking
// teardown.
func checkNoDependentApps(env any) error {
	backend, err := environmentBackend(env)
	if err != nil {
		return err
	}
	if backend == nil {
		fmt.Fprintln(os.Stderr, "Warning: no backend configured -- cannot check for dependent apps. Confirm none exist before continuing.")
		return nil
	}
	envName, err := environmentName(env)
	if err != nil {
		return err
	}

	ctx := context.Background()
	var projectNames []string
	var listErr error

	switch {
	case backend.AWS != nil:
		if client, clientErr := aws.NewS3Client(ctx, backend.AWS.Region); clientErr != nil {
			listErr = clientErr
		} else {
			projectNames, listErr = aws.ListDependentApps(ctx, client, backend.AWS.Bucket, envName)
		}
	case backend.Azure != nil:
		containerURL := fmt.Sprintf("https://%s.blob.core.windows.net/%s", backend.Azure.StorageAccountName, backend.Azure.ContainerName)
		if client, clientErr := azure.NewBlobContainerClient(containerURL); clientErr != nil {
			listErr = clientErr
		} else {
			projectNames, listErr = azure.ListDependentApps(ctx, client, envName)
		}
	case backend.Gcp != nil:
		if client, clientErr := gcp.NewObjectLister(ctx, backend.Gcp.Bucket); clientErr != nil {
			listErr = clientErr
		} else {
			defer client.Close()
			projectNames, listErr = gcp.ListDependentApps(ctx, client, envName)
		}
	default:
		return fmt.Errorf("backend has no aws/azure/gcp block set")
	}

	if listErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not check for dependent apps: %v. Confirm none exist before continuing.\n", listErr)
		return nil
	}

	if len(projectNames) > 0 {
		return fmt.Errorf("%d app(s) still depend on this environment: %s", len(projectNames), strings.Join(projectNames, ", "))
	}
	return nil
}

func init() {
	rootCmd.AddCommand(envDestroyCmd)

	envDestroyCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloudcompose init` (terraform apply must have run there already)")
	envDestroyCmd.Flags().Bool("force", false, "Skip the dependent-app check entirely. Off by default -- normally the check itself, or a human confirming by other means, should establish no apps depend on this environment first.")
	envDestroyCmd.Flags().Bool("auto-approve", false, "Skip the terraform destroy confirmation prompt, for non-interactive callers (CI, scripts). Off by default -- a human should normally review the plan first.")
}
