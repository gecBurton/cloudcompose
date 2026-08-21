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
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/models"
	"github.com/spf13/cobra"
)

// composeLogsCmd shows recent log output for the services in a
// compose file, the way `docker compose logs` shows container logs --
// but fetched directly from the cloud, as a one-shot fetch (not a
// --follow tail) in this first version.
var composeLogsCmd = &cobra.Command{
	Use:   "logs [service...]",
	Short: "Show recent logs for deployed services",
	Long:  "Fetch recent log output directly from the cloud for one or more compose services (AWS and Azure). Shows every service if none are named, like `docker compose logs`.",
	Run:   runComposeLogs,
}

// logEventJSON is the cloud-agnostic shape `logs --json` emits -- one
// entry per log line, regardless of which cloud produced it.
type logEventJSON struct {
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"` // RFC3339, UTC
	Message   string `json:"message"`
}

func runComposeLogs(cmd *cobra.Command, args []string) {
	composeFileFlag, _ := cmd.Flags().GetString("file")
	envDir, _ := cmd.Flags().GetString("env")
	projectName, _ := cmd.Flags().GetString("project")
	since, _ := cmd.Flags().GetDuration("since")
	tail, _ := cmd.Flags().GetInt("tail")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	if envDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --env is required")
		os.Exit(1)
	}

	composeFile, err := resolveComposeFile(composeFileFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
	// Check target support before doing any further work.
	if err := requireAwsOrAzure(cmd.Name(), env); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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

	switch e := env.(type) {
	case *models.AwsEnvironment:
		logsClient, err := aws.NewCloudWatchLogsClient(ctx, e.Region)
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		var sinceMillis int64
		if since > 0 {
			sinceMillis = time.Now().Add(-since).UnixMilli()
		}
		events, err := aws.FetchLogs(ctx, logsClient, semanticApp, e, args, sinceMillis, int32(tail))
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		if jsonOutput {
			printJSONArray(os.Stdout, awsLogEventsJSON(events))
		} else {
			printAwsLogEvents(os.Stdout, events)
		}

	case *models.AzureEnvironment:
		subscriptionID, err := azure.SubscriptionIDFromResourceID(e.LogAnalyticsWorkspaceID)
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		logsClient, err := azure.NewLogsClient()
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		var sinceTime time.Time
		if since > 0 {
			sinceTime = time.Now().Add(-since).UTC()
		}
		events, err := azure.FetchLogs(ctx, logsClient, subscriptionID, semanticApp, e, args, sinceTime, int32(tail))
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		if jsonOutput {
			printJSONArray(os.Stdout, azureLogEventsJSON(events))
		} else {
			printAzureLogEvents(os.Stdout, events)
		}

	default:
		// Unreachable: requireAwsOrAzure above already rejected
		// anything but AWS/Azure.
		target, _ := environmentTarget(env)
		fmt.Fprintf(os.Stderr, "Error: `cloud-compose compose logs` does not support %s environments yet\n", target)
		os.Exit(1)
	}
}

// printAwsLogEvents renders logs the way `docker compose logs` does
// when following more than one service: each line prefixed with the
// service name it came from.
func printAwsLogEvents(w io.Writer, events []aws.LogEvent) {
	for _, e := range events {
		fmt.Fprintln(w, awsLogLine(e))
	}
}

// awsLogLine formats a single aws.LogEvent, matching
// printAwsLogEvents' format.
func awsLogLine(e aws.LogEvent) string {
	ts := time.UnixMilli(e.Timestamp).UTC().Format(time.RFC3339)
	return fmt.Sprintf("%s  %s  | %s", ts, e.Service, e.Message)
}

// awsLogEventsJSON converts aws.LogEvent (epoch millis) into the
// cloud-agnostic logEventJSON shape (RFC3339 string).
func awsLogEventsJSON(events []aws.LogEvent) []logEventJSON {
	rows := make([]logEventJSON, 0, len(events))
	for _, e := range events {
		rows = append(rows, logEventJSON{
			Service:   e.Service,
			Timestamp: time.UnixMilli(e.Timestamp).UTC().Format(time.RFC3339),
			Message:   e.Message,
		})
	}
	return rows
}

// printAzureLogEvents mirrors printAwsLogEvents for Azure's own
// LogEvent shape (a time.Time, not epoch millis).
func printAzureLogEvents(w io.Writer, events []azure.LogEvent) {
	for _, e := range events {
		fmt.Fprintln(w, azureLogLine(e))
	}
}

// azureLogLine formats a single azure.LogEvent, matching
// printAzureLogEvents' format.
func azureLogLine(e azure.LogEvent) string {
	ts := e.Timestamp.UTC().Format(time.RFC3339)
	return fmt.Sprintf("%s  %s  | %s", ts, e.Service, e.Message)
}

// azureLogEventsJSON converts azure.LogEvent into the cloud-agnostic
// logEventJSON shape.
func azureLogEventsJSON(events []azure.LogEvent) []logEventJSON {
	rows := make([]logEventJSON, 0, len(events))
	for _, e := range events {
		rows = append(rows, logEventJSON{
			Service:   e.Service,
			Timestamp: e.Timestamp.UTC().Format(time.RFC3339),
			Message:   e.Message,
		})
	}
	return rows
}

func init() {
	composeCmd.AddCommand(composeLogsCmd)

	composeLogsCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloud-compose env init` (terraform apply must have run there already)")
	composeLogsCmd.Flags().StringP("project", "p", "", "Name of the project (defaults to the directory name)")
	composeLogsCmd.Flags().Duration("since", 0, "Only show logs newer than a relative duration, e.g. 30m, 1h (default: no limit)")
	composeLogsCmd.Flags().Int("tail", 200, "Number of log lines to fetch per service")
	composeLogsCmd.Flags().Bool("json", false, "Output as a JSON array instead of human-readable lines")
}
