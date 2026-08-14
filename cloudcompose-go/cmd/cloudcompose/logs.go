package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gecburton/cloudcompose/internal/compiler"
	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/models"
	"github.com/spf13/cobra"
)

// logsCmd shows recent log output for the services in a compose file,
// the way `docker compose logs` shows container logs -- but for
// whatever the cloud actually logged, not anything derivable from
// compose.yml or Terraform state (see aws.FetchLogs's own doc comment
// for why this is a one-shot fetch, not a --follow tail, in this first
// version).
var logsCmd = &cobra.Command{
	Use:   "logs [service...]",
	Short: "Show recent logs for deployed services",
	Long:  "Fetch recent log output directly from the cloud for one or more compose services (AWS only, for now). Shows every service if none are named, like `docker compose logs`.",
	Run:   runLogs,
}

func runLogs(cmd *cobra.Command, args []string) {
	composeFile, _ := cmd.Flags().GetString("file")
	envDir, _ := cmd.Flags().GetString("env")
	projectName, _ := cmd.Flags().GetString("project")
	since, _ := cmd.Flags().GetDuration("since")
	tail, _ := cmd.Flags().GetInt("tail")

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
		fmt.Fprintf(os.Stderr, "Error: `cloudcompose logs` only supports AWS environments so far (got %s)\n", target)
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
	logsClient, err := aws.NewCloudWatchLogsClient(ctx, awsEnv.Region)
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	var sinceMillis int64
	if since > 0 {
		sinceMillis = time.Now().Add(-since).UnixMilli()
	}

	events, err := aws.FetchLogs(ctx, logsClient, semanticApp, awsEnv, args, sinceMillis, int32(tail))
	if err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}

	printLogEvents(os.Stdout, events)
}

// printLogEvents renders logs output the way `docker compose logs`
// does when following more than one service: each line prefixed with
// the service name it came from, events already in chronological order
// (see aws.FetchLogs's own sort).
func printLogEvents(w io.Writer, events []aws.LogEvent) {
	for _, e := range events {
		fmt.Fprintln(w, logLine(e))
	}
}

// logLine formats a single LogEvent, matching printLogEvents' format.
// Split out so tests can assert on formatting without capturing writer
// output.
func logLine(e aws.LogEvent) string {
	ts := time.UnixMilli(e.Timestamp).UTC().Format(time.RFC3339)
	return fmt.Sprintf("%s  %s  | %s", ts, e.Service, e.Message)
}

func init() {
	rootCmd.AddCommand(logsCmd)

	logsCmd.Flags().StringP("file", "f", "compose.yml", "Path to the Docker Compose file")
	logsCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloudcompose init` (terraform apply must have run there already)")
	logsCmd.Flags().StringP("project", "p", "", "Name of the project (defaults to the directory name)")
	logsCmd.Flags().Duration("since", 0, "Only show logs newer than a relative duration, e.g. 30m, 1h (default: no limit)")
	logsCmd.Flags().Int("tail", 200, "Number of log lines to fetch per service")
}
