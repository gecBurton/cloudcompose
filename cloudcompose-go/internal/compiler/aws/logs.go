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

// FetchLogs returns recent log events for the given container services
// (or every container service in app if services is empty, matching
// `docker compose logs`'s own no-argument behavior of showing
// everything), across the given cluster's environment.
//
// Unlike FetchStatus (status.go), scheduled-task services are not
// excluded here: a service with a Schedule never gets its own
// aws_ecs_service (compute.go's "Only create service if not
// scheduled"), but it still runs as an ECS task on its own schedule and
// logs to the exact same "/ecs/"+getName(service.Name) log group -- the
// log group is created unconditionally in compute.go, before the
// scheduled/unscheduled branch. So logs has no CapabilityContainer-only
// exclusion beyond that; every container-capability service, scheduled
// or not, has a log group worth reading.
//
// Results are merged across services and sorted by timestamp, so
// multi-service output naturally interleaves in chronological order
// (each event still carries its own Service field so the caller can
// prefix each line). A service whose log group doesn't exist yet (never
// deployed, or deployed under a since-changed name) is silently
// skipped, not an error -- the same "not found is not a failure"
// principle status.go's ServiceStatus.Found follows.
func FetchLogs(ctx context.Context, client cloudwatchLogsClient, app *models.Application, env *models.AwsEnvironment, services []string, since int64, limit int32) ([]LogEvent, error) {
	getName := func(resourceName string) string {
		return env.Name + "-" + app.Name + "-" + resourceName
	}

	wanted := make(map[string]bool, len(services))
	for _, s := range services {
		wanted[s] = true
	}

	var events []LogEvent
	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer {
			continue
		}
		if len(wanted) > 0 && !wanted[service.Name] {
			continue
		}

		logGroupName := "/ecs/" + getName(service.Name)
		// The bare service name is also the container name inside the
		// task definition (compute.go's container.Name = service.Name,
		// not getName-prefixed) and therefore the awslogs driver's own
		// stream-name segment, given awslogs-stream-prefix = "ecs"
		// (shared.AWSLogsStreamPrefix): streams look like
		// "ecs/<service.Name>/<task-id>". Filtering by that prefix
		// means a shared log group (if one were ever reused across
		// services, which it currently isn't) couldn't leak another
		// service's lines into this one's output.
		streamPrefix := shared.AWSLogsStreamPrefix + "/" + service.Name + "/"

		input := &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:        aws.String(logGroupName),
			LogStreamNamePrefix: aws.String(streamPrefix),
			Limit:               aws.Int32(limit),
		}
		if since > 0 {
			input.StartTime = aws.Int64(since)
		}

		var nextToken *string
		for {
			input.NextToken = nextToken
			out, err := client.FilterLogEvents(ctx, input)
			if err != nil {
				if isLogGroupNotFound(err) {
					break
				}
				return nil, fmt.Errorf("filter log events for %s: %w", service.Name, err)
			}
			for _, e := range out.Events {
				events = append(events, LogEvent{
					Service:   service.Name,
					Timestamp: aws.ToInt64(e.Timestamp),
					Message:   aws.ToString(e.Message),
				})
			}
			if out.NextToken == nil || len(out.Events) == 0 {
				break
			}
			nextToken = out.NextToken
		}
	}

	sort.Slice(events, func(i, j int) bool { return events[i].Timestamp < events[j].Timestamp })
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
