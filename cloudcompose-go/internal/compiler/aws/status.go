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
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// ServiceStatus is one row of `cloudcompose ps` output: a compose
// service's live status on AWS, or the reason it has none.
type ServiceStatus struct {
	// Name is the compose service name, not the AWS resource name.
	Name string

	// AWSName is the ECS service name ps queried AWS for.
	AWSName string

	// Found is false when ECS has no service by that name. Every other
	// field is meaningless when this is false.
	Found bool

	// Status is ECS's own service status string (ACTIVE/DRAINING/etc.).
	Status string

	DesiredCount int32
	RunningCount int32
	PendingCount int32

	// HasIngress is true when the service has a target group, making
	// Healthy/Unhealthy meaningful.
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
// FetchStatus needs.
type elbClient interface {
	DescribeTargetHealth(ctx context.Context, params *elasticloadbalancingv2.DescribeTargetHealthInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error)
}

// NewAWSClients builds the real AWS SDK clients FetchStatus needs from
// the ambient credential chain.
func NewAWSClients(ctx context.Context, region string) (ecsClient, elbClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, nil, fmt.Errorf("load AWS credentials: %w", err)
	}
	return ecs.NewFromConfig(cfg), elasticloadbalancingv2.NewFromConfig(cfg), nil
}

// FetchStatus queries live ECS/ELB status for every container service in
// app, against the cluster env points at. Batches DescribeServices calls
// (10 services per call, AWS's own limit) and fetches target-group
// health for services with ingress via one DescribeTargetHealth call per
// service.
func FetchStatus(ctx context.Context, ecsC ecsClient, elbC elbClient, app *models.Application, env *models.AwsEnvironment) ([]ServiceStatus, error) {
	getName := shared.ResourceNamer(env.Name, app.Name)

	var containerServices []*models.Service
	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer {
			continue
		}
		if service.Schedule != nil {
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

// targetHealth sums healthy/unhealthy target counts across the target
// groups an ECS service's LoadBalancers list references.
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
