package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/gecburton/cloudcompose/internal/compiler"
	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/models"
	"github.com/spf13/cobra"
)

// psCmd shows live status for the services in a compose file, the way
// `docker compose ps` shows live container status -- but for whatever
// is actually running on the cloud right now, not for anything
// Terraform or compose.yml alone can already tell you (see
// aws.FetchStatus's own doc comment for why this deliberately never
// reads Terraform state/output).
var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "Show live status of deployed services",
	Long:  "Query the cloud directly for each compose service's live running status (AWS only, for now).",
	Run:   runPs,
}

func runPs(cmd *cobra.Command, args []string) {
	composeFile, _ := cmd.Flags().GetString("file")
	envDir, _ := cmd.Flags().GetString("env")
	projectName, _ := cmd.Flags().GetString("project")

	if envDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --env is required")
		os.Exit(1)
	}

	absCompose, err := filepath.Abs(composeFile)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	if _, err := os.Stat(absCompose); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s does not exist or is not readable\n", composeFile)
		os.Exit(1)
	}
	if projectName == "" {
		projectName = filepath.Base(filepath.Dir(absCompose))
	}

	env, err := compiler.LoadEnvironment(envDir)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	awsEnv, ok := env.(*models.AwsEnvironment)
	if !ok {
		target, _ := environmentTarget(env)
		fmt.Fprintf(os.Stderr, "Error: `cloudcompose ps` only supports AWS environments so far (got %s)\n", target)
		os.Exit(1)
	}

	composeApp, err := compiler.ParseCompose(composeFile)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
	semanticApp, err := compiler.Normalize(composeApp, projectName)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	ctx := context.Background()
	ecsClient, elbClient, err := aws.NewAWSClients(ctx, awsEnv.Region)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	statuses, err := aws.FetchStatus(ctx, ecsClient, elbClient, semanticApp, awsEnv)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	printPsTable(os.Stdout, statuses)
}

// printPsTable renders ps output in the same spirit as `docker compose
// ps`: one aligned table, NAME first, a human STATUS summary rather
// than raw counters where possible.
func printPsTable(w io.Writer, statuses []aws.ServiceStatus) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tTASKS\tHEALTH")
	for _, s := range statuses {
		fmt.Fprintln(tw, psRow(s))
	}
	tw.Flush()
}

// psRow formats a single ServiceStatus, matching printPsTable's column
// order. Split out from printPsTable so tests can assert on formatting
// without capturing writer output.
func psRow(s aws.ServiceStatus) string {
	if !s.Found {
		return fmt.Sprintf("%s\tnot found\t-\t-", s.Name)
	}

	tasks := fmt.Sprintf("%d/%d running, %d pending", s.RunningCount, s.DesiredCount, s.PendingCount)

	health := "-"
	if s.HasIngress {
		health = fmt.Sprintf("%d healthy, %d unhealthy", s.Healthy, s.Unhealthy)
	}

	return fmt.Sprintf("%s\t%s\t%s\t%s", s.Name, s.Status, tasks, health)
}

func init() {
	rootCmd.AddCommand(psCmd)

	psCmd.Flags().StringP("file", "f", "compose.yml", "Path to the Docker Compose file")
	psCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloudcompose init` (terraform apply must have run there already)")
	psCmd.Flags().StringP("project", "p", "", "Name of the project (defaults to the directory name)")
}
