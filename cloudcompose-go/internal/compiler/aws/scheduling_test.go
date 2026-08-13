package aws

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// TestEventbridgeExpression_PinnedValues pins each case to a fixed
// expected output, not just this function's own idea of what it should
// produce.
func TestEventbridgeExpression_PinnedValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		schedule models.Schedule
		want     string
	}{
		{"rate 1 hour singular", models.RateSchedule{Value: 1, Unit: models.RateUnitHours}, "rate(1 hour)"},
		{"rate 2 hours plural", models.RateSchedule{Value: 2, Unit: models.RateUnitHours}, "rate(2 hours)"},
		{"rate 30 minutes", models.RateSchedule{Value: 30, Unit: models.RateUnitMinutes}, "rate(30 minutes)"},
		{"cron day-of-week wildcard", models.CronSchedule{Expression: "0 5 * * *"}, "cron(0 5 * * ? *)"},
		{"cron day-of-month wildcard", models.CronSchedule{Expression: "0 5 1 * *"}, "cron(0 5 1 * ? *)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eventbridgeExpression(tc.schedule)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEventbridgeExpression_RejectsBothDayFieldsConstrained checks the
// error returned for a cron expression constraining both day-of-month and
// day-of-week, which EventBridge cannot express.
func TestEventbridgeExpression_RejectsBothDayFieldsConstrained(t *testing.T) {
	t.Parallel()
	_, err := eventbridgeExpression(models.CronSchedule{Expression: "0 5 1 * 2"})
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "schedule \"0 5 1 * 2\" constrains both day-of-month and day-of-week, which EventBridge cannot express: it requires one of them to be unset"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

// TestInferScheduledTasks_CreatesEventBridgeResources exercises the whole
// pipeline (networking -> compute -> scheduling) for a container carrying a
// schedule, checking that it does not produce a persistent ECS service and
// does produce the EventBridge rule/role/policy/target.
func TestInferScheduledTasks_CreatesEventBridgeResources(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{
				Name:       "cleanup",
				Image:      "cleanup:latest",
				Capability: models.CapabilityContainer,
				Size:       models.ServiceSizeSmall,
				MinScale:   1,
				MaxScale:   1,
				Schedule:   models.RateSchedule{Value: 1, Unit: models.RateUnitHours},
			},
		},
	}
	env := fullMockProdEnv()
	getName := minimalGetName("prod", "app")

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, getName, nil)
	priorities := CalculateListenerPriorities(app)
	namespace := InferServiceDiscovery(resources, app, &env, getName, nil)
	InferComputeResources(resources, app, &env, getName, nil, false, priorities, namespace)

	if _, ok := resources.EcsService["cleanup_service"]; ok {
		t.Errorf("did not expect a persistent ECS service for a scheduled task")
	}
	if _, ok := resources.EcsTaskDefinition["cleanup_td"]; !ok {
		t.Fatalf("expected a task definition even for a scheduled task")
	}

	if err := InferScheduledTasks(resources, app, &env, getName, nil); err != nil {
		t.Fatalf("InferScheduledTasks failed: %v", err)
	}

	rule, ok := resources.CloudwatchEventRule["cleanup_rule"]
	if !ok {
		t.Fatalf("expected a cloudwatch event rule, got keys %v", keysOf(resources.CloudwatchEventRule))
	}
	if rule.ScheduleExpression != "rate(1 hour)" {
		t.Errorf("ScheduleExpression = %q, want rate(1 hour)", rule.ScheduleExpression)
	}

	if _, ok := resources.IamRole["cleanup_eb_role"]; !ok {
		t.Errorf("expected an EventBridge IAM role")
	}
	if _, ok := resources.IamRolePolicy["cleanup_eb_policy"]; !ok {
		t.Errorf("expected an EventBridge IAM policy")
	}
	target, ok := resources.CloudwatchEventTarget["cleanup_target"]
	if !ok {
		t.Fatalf("expected a cloudwatch event target")
	}
	if target.Arn != env.EcsClusterArn {
		t.Errorf("target Arn = %q, want %q", target.Arn, env.EcsClusterArn)
	}
}

// TestEventbridgeExpression_AcceptsPointerSchedules checks that
// eventbridgeExpression handles the *models.RateSchedule/*models.
// CronSchedule pointer types the real normalizer actually produces (see
// normalizer.go), not just the value types hand-built tests reach for --
// a real end-to-end run against the production-stack example (which has
// a cron schedule) panicked with "unknown schedule type
// *models.CronSchedule" despite every hand-built value-type test
// passing.
func TestEventbridgeExpression_AcceptsPointerSchedules(t *testing.T) {
	t.Parallel()
	rate := &models.RateSchedule{Value: 1, Unit: models.RateUnitHours}
	if got, err := eventbridgeExpression(rate); err != nil || got != "rate(1 hour)" {
		t.Errorf("got %q, err %v, want rate(1 hour), nil", got, err)
	}

	cron := &models.CronSchedule{Expression: "0 5 * * *"}
	if got, err := eventbridgeExpression(cron); err != nil || got != "cron(0 5 * * ? *)" {
		t.Errorf("got %q, err %v, want cron(0 5 * * ? *), nil", got, err)
	}
}
