// This file adds another concern to the aws package, alongside
// status.go's live `ps` support: live logs ("cloudcompose logs"). Same
// rationale as status.go -- deliberately independent of Terraform
// state/output, recomputing the CloudWatch log group name from the
// same env.Name-app.Name-service.Name formula InferAWS's own getName
// closure uses to create it in the first place (compute.go's
// `logGroup.Name = "/ecs/" + getName(service.Name)`).
package aws

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// LogEvent is one line of `cloudcompose logs` output, tagged with which
// compose service it came from so multi-service output can be
// interleaved with a NAME prefix, the same way `docker compose logs`
// tags each line.
type LogEvent struct {
	Service   string
	Timestamp int64 // milliseconds since the Unix epoch, per CloudWatch's own convention
	Message   string
}

// cloudwatchLogsClient is the subset of *cloudwatchlogs.Client that
// FetchLogs needs, mirroring status.go's ecsClient/elbClient rationale:
// letting tests substitute a fake without a real AWS call.
type cloudwatchLogsClient interface {
	FilterLogEvents(ctx context.Context, params *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
}

// NewCloudWatchLogsClient builds the real CloudWatch Logs client
// FetchLogs needs, from the same ambient credential chain
// NewAWSClients already uses (see its own doc comment) -- kept as a
// separate constructor rather than folded into NewAWSClients so `ps`
// (which never touches logs) doesn't gain an unused dependency on this
// package's logs client.
func NewCloudWatchLogsClient(ctx context.Context, region string) (cloudwatchLogsClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS credentials: %w", err)
	}
	return cloudwatchlogs.NewFromConfig(cfg), nil
}

// FetchLogs returns recent log events for the given services (or every
// container/database service in app if services is empty, matching
// `docker compose logs`'s own no-argument behavior of showing
// everything), across the given cluster's environment.
//
// Covers two service capabilities, each with its own log-group naming
// and behavior:
//
//   - CapabilityContainer: unlike FetchStatus (status.go),
//     scheduled-task services are not excluded here: a service with a
//     Schedule never gets its own aws_ecs_service (compute.go's "Only
//     create service if not scheduled"), but it still runs as an ECS
//     task on its own schedule and logs to the exact same
//     "/ecs/"+getName(service.Name) log group -- the log group is
//     created unconditionally in compute.go, before the
//     scheduled/unscheduled branch.
//   - CapabilityDatabase: RDS exports one log group per engine-specific
//     log type (managed.go's inferDatabase sets
//     EnabledCloudwatchLogsExports unconditionally, so every database
//     this compiler creates has log export on by default -- there is
//     nothing to opt into at query time). Each log group is named
//     "/aws/rds/instance/<identifier>/<log-type>"
//     (AWS's own fixed naming convention for RDS's CloudWatch export,
//     not something cloudcompose chooses), where identifier is the same
//     getName(service.Name) inferDatabase itself used for
//     aws_db_instance.identifier. All of that service's log types are
//     queried and merged together, tagged with the same Service field
//     regardless of which log type (e.g. "error" vs "slowquery") they
//     came from -- a caller wanting to distinguish them can still do so
//     from the message content itself, but `docker compose logs` has no
//     concept of "log type" to expose a separate field for.
//
// Results are merged across services (and across log types, for
// databases) and sorted by timestamp, so multi-service output naturally
// interleaves in chronological order (each event still carries its own
// Service field so the caller can prefix each line). A service whose
// log group doesn't exist yet (never deployed, or deployed under a
// since-changed name) is silently skipped, not an error -- the same
// "not found is not a failure" principle status.go's ServiceStatus.Found
// follows.
func FetchLogs(ctx context.Context, client cloudwatchLogsClient, app *models.Application, env *models.AwsEnvironment, services []string, since int64, limit int32) ([]LogEvent, error) {
	getName := shared.ResourceNamer(env.Name, app.Name)

	wanted := make(map[string]bool, len(services))
	for _, s := range services {
		wanted[s] = true
	}

	var events []LogEvent
	for i := range app.Services {
		service := &app.Services[i]
		if len(wanted) > 0 && !wanted[service.Name] {
			continue
		}

		switch service.Capability {
		case models.CapabilityContainer:
			serviceEvents, err := fetchContainerLogs(ctx, client, service, getName, since, limit)
			if err != nil {
				return nil, err
			}
			events = append(events, serviceEvents...)

		case models.CapabilityDatabase:
			serviceEvents, err := fetchDatabaseLogs(ctx, client, service, getName, since, limit)
			if err != nil {
				return nil, err
			}
			events = append(events, serviceEvents...)
		}
	}

	sort.Slice(events, func(i, j int) bool { return events[i].Timestamp < events[j].Timestamp })
	return events, nil
}

// fetchContainerLogs queries the single ECS log group a container
// service logs to. Split out of FetchLogs so that function's own
// per-capability switch stays a plain dispatch, matching
// fetchDatabaseLogs' own shape below.
func fetchContainerLogs(ctx context.Context, client cloudwatchLogsClient, service *models.Service, getName func(string) string, since int64, limit int32) ([]LogEvent, error) {
	logGroupName := "/ecs/" + getName(service.Name)
	// The bare service name is also the container name inside the task
	// definition (compute.go's container.Name = service.Name, not
	// getName-prefixed) and therefore the awslogs driver's own
	// stream-name segment, given awslogs-stream-prefix = "ecs"
	// (shared.AWSLogsStreamPrefix): streams look like
	// "ecs/<service.Name>/<task-id>". Filtering by that prefix means a
	// shared log group (if one were ever reused across services, which
	// it currently isn't) couldn't leak another service's lines into
	// this one's output.
	streamPrefix := shared.AWSLogsStreamPrefix + "/" + service.Name + "/"
	return fetchLogGroupEvents(ctx, client, service.Name, logGroupName, &streamPrefix, since, limit)
}

// fetchDatabaseLogs queries every RDS log group this service's engine
// exports (see FetchLogs's own doc comment for the naming convention
// and why there's one log group per log type, not one for the whole
// database).
func fetchDatabaseLogs(ctx context.Context, client cloudwatchLogsClient, service *models.Service, getName func(string) string, since int64, limit int32) ([]LogEvent, error) {
	engine := "postgres"
	imageLower := strings.ToLower(service.Image)
	if strings.Contains(imageLower, "mysql") {
		engine = "mysql"
	} else if strings.Contains(imageLower, "mariadb") {
		engine = "mariadb"
	}

	identifier := getName(service.Name)
	var events []LogEvent
	for _, logType := range shared.RDSLogExports[engine] {
		logGroupName := fmt.Sprintf("/aws/rds/instance/%s/%s", identifier, logType)
		logTypeEvents, err := fetchLogGroupEvents(ctx, client, service.Name, logGroupName, nil, since, limit)
		if err != nil {
			return nil, err
		}
		events = append(events, logTypeEvents...)
	}
	return events, nil
}

// fetchLogGroupEvents runs FilterLogEvents against one log group,
// paginating until exhausted, tagging every result with serviceName --
// the shared plumbing both fetchContainerLogs and fetchDatabaseLogs
// build on. streamNamePrefix is optional (nil for RDS, which has no
// stream-prefix concept the way awslogs-stream-prefix gives ECS one).
func fetchLogGroupEvents(ctx context.Context, client cloudwatchLogsClient, serviceName, logGroupName string, streamNamePrefix *string, since int64, limit int32) ([]LogEvent, error) {
	input := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:        aws.String(logGroupName),
		LogStreamNamePrefix: streamNamePrefix,
		Limit:               aws.Int32(limit),
	}
	if since > 0 {
		input.StartTime = aws.Int64(since)
	}

	var events []LogEvent
	var nextToken *string
	for {
		input.NextToken = nextToken
		out, err := client.FilterLogEvents(ctx, input)
		if err != nil {
			if isLogGroupNotFound(err) {
				break
			}
			return nil, fmt.Errorf("filter log events for %s: %w", serviceName, err)
		}
		for _, e := range out.Events {
			events = append(events, LogEvent{
				Service:   serviceName,
				Timestamp: aws.ToInt64(e.Timestamp),
				Message:   aws.ToString(e.Message),
			})
		}
		if out.NextToken == nil || len(out.Events) == 0 {
			break
		}
		nextToken = out.NextToken
	}
	return events, nil
}

// isLogGroupNotFound reports whether err is CloudWatch Logs' own
// ResourceNotFoundException, meaning the log group itself doesn't
// exist -- expected for a service that's never been deployed, not a
// real failure `logs` should surface as one.
func isLogGroupNotFound(err error) bool {
	var notFound *cwltypes.ResourceNotFoundException
	return errors.As(err, &notFound)
}
