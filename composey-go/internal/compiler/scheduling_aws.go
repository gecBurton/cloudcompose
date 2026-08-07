package compiler

import (
	"fmt"
	"strings"

	"github.com/gecburton/composey/internal/models"
)

// InferScheduledTasks infers EventBridge scheduled task resources,
// mirroring _scheduling.py's infer_scheduled_tasks.
//
// Services with a schedule do not run as persistent ECS services. They are
// triggered as standalone tasks via EventBridge.
func InferScheduledTasks(
	resources *models.AWSResources,
	app *models.Application,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
) error {
	for i := range app.Services {
		service := &app.Services[i]
		if service.Schedule == nil || service.Capability != models.CapabilityContainer {
			continue
		}

		expression, err := eventbridgeExpression(service.Schedule)
		if err != nil {
			return err
		}

		// Create EventBridge rule.
		ruleKey := service.Name + "_rule"
		rule := models.NewCloudwatchEventRule()
		rule.Name = getName(service.Name + "-rule")
		rule.ScheduleExpression = expression
		desc := fmt.Sprintf("Schedule for %s", service.Name)
		rule.Description = &desc
		rule.Tags = tags
		resources.CloudwatchEventRule[ruleKey] = rule

		// Create IAM role for EventBridge.
		ebRoleKey := service.Name + "_eb_role"
		assumeRolePolicy := PyDumps(PyOrdered{
			p("Version", IAMPolicyVersion),
			p("Statement", []PyOrdered{
				{
					p("Action", "sts:AssumeRole"),
					p("Effect", "Allow"),
					p("Principal", PyOrdered{p("Service", "events.amazonaws.com")}),
				},
			}),
		})
		resources.IamRole[ebRoleKey] = models.IamRole{
			Name:             getName(service.Name + "-eb-role"),
			AssumeRolePolicy: assumeRolePolicy,
			Tags:             tags,
		}

		// Create IAM policy for EventBridge to run ECS tasks.
		taskDefKey := service.Name + "_td"
		ebPolicyKey := service.Name + "_eb_policy"
		policy := PyDumps(PyOrdered{
			p("Version", IAMPolicyVersion),
			p("Statement", []PyOrdered{
				{
					p("Effect", "Allow"),
					p("Action", "ecs:RunTask"),
					p("Resource", []string{fmt.Sprintf("${aws_ecs_task_definition.%s.arn}", taskDefKey)}),
					p("Condition", PyOrdered{
						p("ArnLike", PyOrdered{p("ecs:cluster", env.EcsClusterArn)}),
					}),
				},
				{
					p("Effect", "Allow"),
					p("Action", "iam:PassRole"),
					p("Resource", []string{"*"}),
					p("Condition", PyOrdered{
						p("StringLike", PyOrdered{p("iam:PassedToService", "ecs-tasks.amazonaws.com")}),
					}),
				},
			}),
		})
		resources.IamRolePolicy[ebPolicyKey] = models.IamRolePolicy{
			Name:   getName(service.Name + "-eb-policy"),
			Role:   fmt.Sprintf("${aws_iam_role.%s.name}", ebRoleKey),
			Policy: policy,
		}

		// Create EventBridge target.
		roleArn := fmt.Sprintf("${aws_iam_role.%s.arn}", ebRoleKey)
		resources.CloudwatchEventTarget[service.Name+"_target"] = models.CloudwatchEventTarget{
			Rule:    fmt.Sprintf("${aws_cloudwatch_event_rule.%s.name}", ruleKey),
			Arn:     env.EcsClusterArn,
			RoleArn: &roleArn,
			EcsTarget: map[string]any{
				"task_count":          1,
				"task_definition_arn": fmt.Sprintf("${aws_ecs_task_definition.%s.arn}", taskDefKey),
				"launch_type":         "FARGATE",
				"network_configuration": map[string]any{
					"subnets":          env.PrivateSubnets,
					"security_groups":  SecurityGroupIDs(service.NetworkIsolationSegments),
					"assign_public_ip": false,
				},
			},
		}
	}

	return nil
}

// eventbridgeExpression renders a cloud-neutral schedule as an EventBridge
// schedule expression, mirroring _scheduling.py's _eventbridge_expression.
//
// EventBridge cron takes six fields rather than the standard five (it adds
// a year), and requires exactly one of day-of-month and day-of-week to be
// the '?' placeholder rather than '*'.
func eventbridgeExpression(schedule models.Schedule) (string, error) {
	switch s := schedule.(type) {
	case models.RateSchedule:
		return rateExpression(s), nil
	case *models.RateSchedule:
		return rateExpression(*s), nil
	case models.CronSchedule:
		return cronExpression(s)
	case *models.CronSchedule:
		return cronExpression(*s)
	default:
		return "", fmt.Errorf("unknown schedule type %T", schedule)
	}
}

func rateExpression(rate models.RateSchedule) string {
	// rate(1 hour) is singular, rate(2 hours) is plural.
	unit := string(rate.Unit)
	if rate.Value == 1 {
		unit = strings.TrimSuffix(unit, "s")
	}
	return fmt.Sprintf("rate(%d %s)", rate.Value, unit)
}

func cronExpression(cron models.CronSchedule) (string, error) {
	fields := strings.Fields(cron.Expression)
	if len(fields) != 5 {
		return "", fmt.Errorf("cron expression %q must have exactly 5 fields", cron.Expression)
	}
	minute, hour, dayOfMonth, month, dayOfWeek := fields[0], fields[1], fields[2], fields[3], fields[4]

	if dayOfWeek == "*" {
		dayOfWeek = "?"
	} else if dayOfMonth == "*" {
		dayOfMonth = "?"
	} else {
		return "", fmt.Errorf(
			"schedule %q constrains both day-of-month and day-of-week, which "+
				"EventBridge cannot express: it requires one of them to be unset",
			cron.Expression,
		)
	}

	return fmt.Sprintf("cron(%s %s %s %s %s *)", minute, hour, dayOfMonth, month, dayOfWeek), nil
}
