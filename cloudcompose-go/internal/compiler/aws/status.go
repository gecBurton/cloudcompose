// Package aws contains AWS-specific inference and Terraform generation.
// This file adds a separate concern: live status ("cloudcompose ps"),
// entirely independent of the parse/normalize/infer/generate pipeline
// used everywhere else in this package. It never touches Terraform
// state or output -- see status.go's own doc comment for why.
package aws

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/gecburton/cloudcompose/internal/models"
)

// ServiceStatus is one row of `cloudcompose ps` output: a compose
// service's live status on AWS, or the reason it has none.
type ServiceStatus struct {
	// Name is the compose service name (e.g. "web"), not the AWS
	// resource name -- ps is keyed by what the user wrote in
	// compose.yml, matching `docker compose ps`'s own NAME column.
	Name string

	// AWSName is the ECS service name ps computed and queried AWS for
	// (env.Name-app.Name-service.Name, the same formula InferAWS's own
	// getName closure uses -- see status.go's doc comment for why this
	// is recomputed rather than read from anywhere).
	AWSName string

	// Found is false when ECS has no service by that name at all: not
	// yet deployed, or deployed under a name that no longer matches
	// (e.g. the environment or project name changed since). Every
	// other field is meaningless when this is false.
	Found bool

	// Status is ECS's own service status string (ACTIVE/DRAINING/etc.),
	// verbatim from DescribeServices.
	Status string

	DesiredCount int32
	RunningCount int32
	PendingCount int32

	// HasIngress is true when the compose service declared a port that
	// InferAWS would have wired to the shared ALB -- see
	// createEcsService/handleIngress in compute.go. Determines whether
	// Healthy/Unhealthy below are meaningful at all: a service with no
	// ingress has no target group, so those fields stay zero.
	HasIngress bool
	Healthy    int
	Unhealthy  int
}

// ecsClient is the subset of *ecs.Client that FetchStatus needs,
// letting tests substitute a fake without spinning up real AWS calls.
type ecsClient interface {
	DescribeServices(ctx context.Context, params *ecs.DescribeServicesInput, optFns ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error)
}

// elbClient is the subset of *elasticloadbalancingv2.Client that
// FetchStatus needs, mirroring ecsClient's rationale.
type elbClient interface {
	DescribeTargetHealth(ctx context.Context, params *elasticloadbalancingv2.DescribeTargetHealthInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error)
}

// NewAWSClients builds the real AWS SDK clients FetchStatus needs from
// the ambient credential chain (environment variables, shared config/
// credentials files, EC2/ECS instance role, or SSO -- whatever
// config.LoadDefaultConfig already knows how to find; see AGENTS.md's
// note that this is the same credential surface CI's
// aws-actions/configure-aws-credentials and local aws-vault already
// populate, so ps needs no new auth code of its own).
func NewAWSClients(ctx context.Context, region string) (ecsClient, elbClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, nil, fmt.Errorf("load AWS credentials: %w", err)
	}
	return ecs.NewFromConfig(cfg), elasticloadbalancingv2.NewFromConfig(cfg), nil
}

// FetchStatus queries live ECS/ELB status for every container service in
// app, against the cluster env points at.
//
// Deliberately independent of Terraform state/output: env.Name and
// app.Name are exactly the same two inputs InferAWS's own getName
// closure combines (env.Name + "-" + app.Name + "-" + resourceName,
// infer.go) to name every AWS resource cloudcompose creates, so ps
// recomputes the ECS service name itself rather than reading it back
// from a `terraform show -json` state file or a Terraform output --
// there is no such output today (see PR discussion), and even if there
// were, it would just be re-stating what compose.yml + environment.yaml
// already imply. Only compose.yml's *runtime* status is genuinely new
// information ps can offer; anything else it could show is already
// fully determined by the static inputs the user already has.
//
// One DescribeServices call covers up to 10 services (AWS's own limit);
// FetchStatus batches automatically. Target-group health for services
// with ingress is fetched with one DescribeTargetHealth call per
// service, chained off the target group ARN DescribeServices itself
// returns -- not by re-deriving the target group's name, which has its
// own 32-character truncation risk compute.go's own naming doesn't
// guard against (see handleIngress's tg.Name = getName(service.Name +
// "-tg")).
func FetchStatus(ctx context.Context, ecsC ecsClient, elbC elbClient, app *models.Application, env *models.AwsEnvironment) ([]ServiceStatus, error) {
	getName := func(resourceName string) string {
		return env.Name + "-" + app.Name + "-" + resourceName
	}

	var containerServices []*models.Service
	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer {
			continue
		}
		if service.Schedule != nil {
			// Scheduled tasks never get an ECS service of their own
			// (see compute.go: "Only create service if not
			// scheduled.") -- nothing for ps to query.
			continue
		}
		containerServices = append(containerServices, service)
	}

	statuses := make(map[string]*ServiceStatus, len(containerServices))
	awsNameToService := make(map[string]string, len(containerServices))
	for _, service := range containerServices {
		awsName := getName(service.Name)
		statuses[service.Name] = &ServiceStatus{
			Name:       service.Name,
			AWSName:    awsName,
			HasIngress: service.Ingress != nil,
		}
		awsNameToService[awsName] = service.Name
	}

	// DescribeServices accepts at most 10 names per call.
	const batchSize = 10
	awsNames := make([]string, 0, len(containerServices))
	for _, service := range containerServices {
		awsNames = append(awsNames, getName(service.Name))
	}

	var describedServices []ecstypes.Service
	for start := 0; start < len(awsNames); start += batchSize {
		end := min(start+batchSize, len(awsNames))
		out, err := ecsC.DescribeServices(ctx, &ecs.DescribeServicesInput{
			Cluster:  aws.String(env.EcsClusterArn),
			Services: awsNames[start:end],
		})
		if err != nil {
			return nil, fmt.Errorf("describe ECS services: %w", err)
		}
		describedServices = append(describedServices, out.Services...)
	}

	for _, svc := range describedServices {
		if svc.ServiceName == nil {
			continue
		}
		serviceName, ok := awsNameToService[*svc.ServiceName]
		if !ok {
			continue
		}
		status := statuses[serviceName]
		status.Found = true
		status.Status = aws.ToString(svc.Status)
		status.DesiredCount = svc.DesiredCount
		status.RunningCount = svc.RunningCount
		status.PendingCount = svc.PendingCount

		if status.HasIngress {
			healthy, unhealthy, err := targetHealth(ctx, elbC, svc.LoadBalancers)
			if err != nil {
				return nil, fmt.Errorf("describe target health for %s: %w", serviceName, err)
			}
			status.Healthy = healthy
			status.Unhealthy = unhealthy
		}
	}

	result := make([]ServiceStatus, 0, len(containerServices))
	for _, service := range containerServices {
		result = append(result, *statuses[service.Name])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// targetHealth sums healthy/unhealthy target counts across every target
// group an ECS service's own LoadBalancers list references (ordinarily
// just one, but the API allows more).
func targetHealth(ctx context.Context, elbC elbClient, loadBalancers []ecstypes.LoadBalancer) (healthy, unhealthy int, err error) {
	for _, lb := range loadBalancers {
		if lb.TargetGroupArn == nil {
			continue
		}
		out, err := elbC.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{
			TargetGroupArn: lb.TargetGroupArn,
		})
		if err != nil {
			return 0, 0, err
		}
		for _, desc := range out.TargetHealthDescriptions {
			if desc.TargetHealth == nil {
				continue
			}
			if desc.TargetHealth.State == elbv2types.TargetHealthStateEnumHealthy {
				healthy++
			} else {
				unhealthy++
			}
		}
	}
	return healthy, unhealthy, nil
}
