package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// fakeECSClient is a minimal in-memory stand-in for *ecs.Client, keyed
// by service name so tests can assert FetchStatus queried exactly the
// AWS-side names InferAWS's own getName closure would have created,
// without making a real AWS call.
type fakeECSClient struct {
	services map[string]ecstypes.Service
	// clusterSeen records every Cluster value DescribeServices was
	// called with, so tests can assert FetchStatus pointed at
	// env.EcsClusterArn and nowhere else.
	clusterSeen []string
}

func (f *fakeECSClient) DescribeServices(ctx context.Context, params *ecs.DescribeServicesInput, optFns ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	f.clusterSeen = append(f.clusterSeen, aws.ToString(params.Cluster))
	out := &ecs.DescribeServicesOutput{}
	for _, name := range params.Services {
		if svc, ok := f.services[name]; ok {
			out.Services = append(out.Services, svc)
		}
	}
	return out, nil
}

// fakeELBClient is a minimal in-memory stand-in for
// *elasticloadbalancingv2.Client, keyed by target group ARN.
type fakeELBClient struct {
	targetHealth map[string][]elbv2types.TargetHealthDescription
}

func (f *fakeELBClient) DescribeTargetHealth(ctx context.Context, params *elasticloadbalancingv2.DescribeTargetHealthInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error) {
	return &elasticloadbalancingv2.DescribeTargetHealthOutput{
		TargetHealthDescriptions: f.targetHealth[aws.ToString(params.TargetGroupArn)],
	}, nil
}

// TestFetchStatus_RealHelloExample exercises the real hello example
// (one public "web" service) through the real parser/normalizer
// boundary, per this codebase's own real-boundary testing discipline
// (AGENTS.md).
func TestFetchStatus_RealHelloExample(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "hello")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := fullMockProdEnv()

	// "prod-hello-web" is exactly what InferAWS's own getName closure
	// would compute for this env/app/service combination -- see
	// infer.go's getName and compute_test.go's own assertion that the
	// real task definition family comes out as "prod-hello-web".
	tgArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/prod-hello-web-tg/abc"
	ecsC := &fakeECSClient{
		services: map[string]ecstypes.Service{
			"prod-hello-web": {
				ServiceName:  aws.String("prod-hello-web"),
				Status:       aws.String("ACTIVE"),
				DesiredCount: 2,
				RunningCount: 2,
				PendingCount: 0,
				LoadBalancers: []ecstypes.LoadBalancer{
					{TargetGroupArn: aws.String(tgArn)},
				},
			},
		},
	}
	elbC := &fakeELBClient{
		targetHealth: map[string][]elbv2types.TargetHealthDescription{
			tgArn: {
				{TargetHealth: &elbv2types.TargetHealth{State: elbv2types.TargetHealthStateEnumHealthy}},
				{TargetHealth: &elbv2types.TargetHealth{State: elbv2types.TargetHealthStateEnumHealthy}},
			},
		},
	}

	statuses, err := FetchStatus(context.Background(), ecsC, elbC, app, &env)
	if err != nil {
		t.Fatalf("FetchStatus failed: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected exactly 1 status, got %d: %+v", len(statuses), statuses)
	}

	got := statuses[0]
	if got.Name != "web" {
		t.Errorf("Name = %q, want web", got.Name)
	}
	if got.AWSName != "prod-hello-web" {
		t.Errorf("AWSName = %q, want prod-hello-web", got.AWSName)
	}
	if !got.Found {
		t.Fatal("expected Found = true")
	}
	if got.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", got.Status)
	}
	if got.DesiredCount != 2 || got.RunningCount != 2 || got.PendingCount != 0 {
		t.Errorf("counts = %d/%d/%d, want 2/2/0", got.DesiredCount, got.RunningCount, got.PendingCount)
	}
	if !got.HasIngress {
		t.Error("expected HasIngress = true (hello's web service declares a port)")
	}
	if got.Healthy != 2 || got.Unhealthy != 0 {
		t.Errorf("health = %d/%d, want 2/0", got.Healthy, got.Unhealthy)
	}

	if len(ecsC.clusterSeen) != 1 || ecsC.clusterSeen[0] != env.EcsClusterArn {
		t.Errorf("DescribeServices called with cluster %v, want exactly [%s]", ecsC.clusterSeen, env.EcsClusterArn)
	}
}

// TestFetchStatus_NotYetDeployed confirms a service ECS has never heard
// of (not yet deployed, or deployed under a name that no longer
// matches) is reported as not-found rather than causing an error.
func TestFetchStatus_NotYetDeployed(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "hello")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := fullMockProdEnv()

	ecsC := &fakeECSClient{services: map[string]ecstypes.Service{}}
	elbC := &fakeELBClient{}

	statuses, err := FetchStatus(context.Background(), ecsC, elbC, app, &env)
	if err != nil {
		t.Fatalf("FetchStatus failed: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected exactly 1 status, got %d", len(statuses))
	}
	if statuses[0].Found {
		t.Error("expected Found = false for a service ECS has no record of")
	}
}

// TestFetchStatus_SkipsScheduledAndNonContainerServices confirms ps
// only ever asks ECS about services InferAWS itself would actually
// create an aws_ecs_service for -- scheduled tasks and non-container
// capabilities (database/cache/object-storage) never get one (see
// compute.go's own "Only create service if not scheduled" and its
// CapabilityContainer filter), so ps has nothing to query for them.
func TestFetchStatus_SkipsScheduledAndNonContainerServices(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/nginx-flask-mysql/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "nginx-flask-mysql")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := fullMockProdEnv()

	ecsC := &fakeECSClient{services: map[string]ecstypes.Service{}}
	elbC := &fakeELBClient{}

	statuses, err := FetchStatus(context.Background(), ecsC, elbC, app, &env)
	if err != nil {
		t.Fatalf("FetchStatus failed: %v", err)
	}

	for _, s := range statuses {
		for i := range app.Services {
			if app.Services[i].Name == s.Name && app.Services[i].Capability != models.CapabilityContainer {
				t.Errorf("expected no status row for non-container service %q", s.Name)
			}
		}
	}
}
