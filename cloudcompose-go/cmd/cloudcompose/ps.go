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
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/models"
	"github.com/spf13/cobra"
)

// psCmd shows live status for the services in a compose file, the way
// `docker compose ps` shows live container status -- but queried
// directly from the cloud, not from Terraform state or compose.yml.
var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "Show live status of deployed services",
	Long:  "Query the cloud directly for each compose service's live running status (AWS and Azure).",
	Run:   runPs,
}

// psRowJSON is the cloud-agnostic shape `ps --json` emits -- one row
// per compose service, regardless of which cloud produced it.
type psRowJSON struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Status  string `json:"status"`
	Running int32  `json:"running"`
	Health  string `json:"health,omitempty"`
}

func runPs(cmd *cobra.Command, args []string) {
	composeFileFlag, _ := cmd.Flags().GetString("file")
	envDir, _ := cmd.Flags().GetString("env")
	projectName, _ := cmd.Flags().GetString("project")
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
		ecsClient, elbClient, err := aws.NewAWSClients(ctx, e.Region)
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		statuses, err := aws.FetchStatus(ctx, ecsClient, elbClient, semanticApp, e)
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		if jsonOutput {
			printJSONArray(os.Stdout, awsPsRowsJSON(statuses))
		} else {
			printAwsPsTable(os.Stdout, statuses)
		}

	case *models.AzureEnvironment:
		subscriptionID, err := azure.SubscriptionIDFromResourceID(e.LogAnalyticsWorkspaceID)
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		appsClient, revClient, err := azure.NewAzureClients(subscriptionID)
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		statuses, err := azure.FetchStatus(ctx, appsClient, revClient, semanticApp, e)
		if err != nil {
			printUnexpectedError(err)
			os.Exit(1)
		}
		if jsonOutput {
			printJSONArray(os.Stdout, azurePsRowsJSON(statuses))
		} else {
			printAzurePsTable(os.Stdout, statuses)
		}

	default:
		// Unreachable: requireAwsOrAzure above already rejected
		// anything but AWS/Azure.
		target, _ := environmentTarget(env)
		fmt.Fprintf(os.Stderr, "Error: `cloudcompose ps` does not support %s environments yet\n", target)
		os.Exit(1)
	}
}

// printAwsPsTable renders AWS ps output as an aligned table, NAME
// first, with a human STATUS summary rather than raw counters.
func printAwsPsTable(w io.Writer, statuses []aws.ServiceStatus) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tTASKS\tHEALTH")
	for _, s := range statuses {
		fmt.Fprintln(tw, psRow(s))
	}
	tw.Flush()
}

// psRow formats a single aws.ServiceStatus, matching printAwsPsTable's
// column order.
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

// awsPsRowsJSON converts aws.ServiceStatus into the cloud-agnostic
// psRowJSON shape.
func awsPsRowsJSON(statuses []aws.ServiceStatus) []psRowJSON {
	rows := make([]psRowJSON, 0, len(statuses))
	for _, s := range statuses {
		row := psRowJSON{Name: s.Name, Found: s.Found}
		if s.Found {
			row.Status = s.Status
			row.Running = s.RunningCount
			if s.HasIngress {
				row.Health = fmt.Sprintf("%d healthy, %d unhealthy", s.Healthy, s.Unhealthy)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// printAzurePsTable renders Azure ps output, mirroring
// printAwsPsTable's shape as closely as Container Apps' status model
// allows.
func printAzurePsTable(w io.Writer, statuses []azure.ServiceStatus) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tREPLICAS\tHEALTH")
	for _, s := range statuses {
		fmt.Fprintln(tw, azurePsRow(s))
	}
	tw.Flush()
}

// azurePsRow formats a single azure.ServiceStatus, matching
// printAzurePsTable's column order.
func azurePsRow(s azure.ServiceStatus) string {
	if !s.Found {
		return fmt.Sprintf("%s\tnot found\t-\t-", s.Name)
	}

	health := "-"
	if s.HealthState != "" {
		health = s.HealthState
	}

	return fmt.Sprintf("%s\t%s\t%d\t%s", s.Name, s.ProvisioningState, s.Replicas, health)
}

// azurePsRowsJSON converts azure.ServiceStatus into the cloud-agnostic
// psRowJSON shape.
func azurePsRowsJSON(statuses []azure.ServiceStatus) []psRowJSON {
	rows := make([]psRowJSON, 0, len(statuses))
	for _, s := range statuses {
		row := psRowJSON{Name: s.Name, Found: s.Found}
		if s.Found {
			row.Status = s.ProvisioningState
			row.Running = s.Replicas
			row.Health = s.HealthState
		}
		rows = append(rows, row)
	}
	return rows
}

func init() {
	rootCmd.AddCommand(psCmd)

	psCmd.Flags().StringP("env", "e", "", "Path to the environment directory created by `cloudcompose init` (terraform apply must have run there already)")
	psCmd.Flags().StringP("project", "p", "", "Name of the project (defaults to the directory name)")
	psCmd.Flags().Bool("json", false, "Output as a JSON array instead of a human-readable table")
}
