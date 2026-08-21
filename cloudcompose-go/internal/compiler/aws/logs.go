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
// compose service it came from.
type LogEvent struct {
	Service   string
	Timestamp int64 // milliseconds since the Unix epoch
	Message   string
}

// cloudwatchLogsClient is the subset of *cloudwatchlogs.Client that
// FetchLogs needs, letting tests substitute a fake without a real AWS
// call.
type cloudwatchLogsClient interface {
	FilterLogEvents(ctx context.Context, params *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
}

// NewCloudWatchLogsClient builds the real CloudWatch Logs client
// FetchLogs needs from the ambient credential chain.
func NewCloudWatchLogsClient(ctx context.Context, region string) (cloudwatchLogsClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS credentials: %w", err)
	}
	return cloudwatchlogs.NewFromConfig(cfg), nil
}

// FetchLogs returns recent log events for the given services (or every
// container/database service in app if services is empty), across
// env's cluster.
//
// For CapabilityContainer, scheduled-task services are included since
// they still log to the same CloudWatch log group as a regular ECS
// service. For CapabilityDatabase, all of an engine's exported RDS log
// types are queried and merged under one Service tag.
//
// Results are merged and sorted by timestamp. A service whose log
// group doesn't exist yet is silently skipped, not an error.
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
// service logs to.
func fetchContainerLogs(ctx context.Context, client cloudwatchLogsClient, service *models.Service, getName func(string) string, since int64, limit int32) ([]LogEvent, error) {
	logGroupName := "/ecs/" + getName(service.Name)
	// The bare service name is the container name and therefore the
	// awslogs stream-name segment: streams look like
	// "ecs/<service.Name>/<task-id>".
	streamPrefix := shared.AWSLogsStreamPrefix + "/" + service.Name + "/"
	return fetchLogGroupEvents(ctx, client, service.Name, logGroupName, &streamPrefix, since, limit)
}

// fetchDatabaseLogs queries every RDS log group this service's engine
// exports, named "/aws/rds/instance/<identifier>/<log-type>".
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
// paginating until exhausted and tagging every result with serviceName.
// streamNamePrefix is optional (nil for RDS).
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

// isLogGroupNotFound reports whether err is CloudWatch Logs'
// ResourceNotFoundException, meaning the log group doesn't exist --
// expected for a service that's never been deployed, not a failure.
func isLogGroupNotFound(err error) bool {
	var notFound *cwltypes.ResourceNotFoundException
	return errors.As(err, &notFound)
}
