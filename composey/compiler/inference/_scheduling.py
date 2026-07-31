"""Scheduled tasks inference (EventBridge).

Handles inference of cron-like scheduled tasks using AWS EventBridge.
"""

import json
from typing import Callable

from composey.constants import IAM_POLICY_VERSION
from composey.exceptions import ScheduleError
from composey.models.aws import (
    AWSResources,
    CloudwatchEventRule,
    CloudwatchEventTarget,
    IamRole,
    IamRolePolicy,
)
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application as SemanticApp
from composey.models.semantic import Schedule

from ._connectivity import security_group_ids


def infer_scheduled_tasks(
    resources: AWSResources,
    app: SemanticApp,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
    discard: bool,
) -> None:
    """Infer EventBridge scheduled task resources.

    Services with a schedule do not run as persistent ECS services.
    They are triggered as standalone tasks via EventBridge.
    """
    for service in app.services:
        if not service.schedule or service.capability != "container":
            continue

        # Create EventBridge rule
        rule_key = f"{service.name}_rule"
        resources.aws_cloudwatch_event_rule[rule_key] = CloudwatchEventRule(
            name=get_name(f"{service.name}-rule"),
            schedule_expression=_eventbridge_expression(service.schedule),
            description=f"Schedule for {service.name}",
            tags=tags,
        )

        # Create IAM role for EventBridge
        eb_role_key = f"{service.name}_eb_role"
        resources.aws_iam_role[eb_role_key] = IamRole(
            name=get_name(f"{service.name}-eb-role"),
            assume_role_policy=json.dumps(
                {
                    "Version": IAM_POLICY_VERSION,
                    "Statement": [
                        {
                            "Action": "sts:AssumeRole",
                            "Effect": "Allow",
                            "Principal": {"Service": "events.amazonaws.com"},
                        }
                    ],
                }
            ),
            tags=tags,
        )

        # Create IAM policy for EventBridge to run ECS tasks
        task_def_key = f"{service.name}_td"
        eb_policy_key = f"{service.name}_eb_policy"
        resources.aws_iam_role_policy[eb_policy_key] = IamRolePolicy(
            name=get_name(f"{service.name}-eb-policy"),
            role=f"${{aws_iam_role.{eb_role_key}.name}}",
            policy=json.dumps(
                {
                    "Version": IAM_POLICY_VERSION,
                    "Statement": [
                        {
                            "Effect": "Allow",
                            "Action": "ecs:RunTask",
                            "Resource": [
                                f"${{aws_ecs_task_definition.{task_def_key}.arn}}"
                            ],
                            "Condition": {
                                "ArnLike": {"ecs:cluster": f"{env.ecs_cluster_arn}"}
                            },
                        },
                        {
                            "Effect": "Allow",
                            "Action": "iam:PassRole",
                            "Resource": ["*"],
                            "Condition": {
                                "StringLike": {
                                    "iam:PassedToService": "ecs-tasks.amazonaws.com"
                                }
                            },
                        },
                    ],
                }
            ),
        )

        # Create EventBridge target
        resources.aws_cloudwatch_event_target[f"{service.name}_target"] = (
            CloudwatchEventTarget(
                rule=f"${{aws_cloudwatch_event_rule.{rule_key}.name}}",
                arn=env.ecs_cluster_arn,
                role_arn=f"${{aws_iam_role.{eb_role_key}.arn}}",
                ecs_target={
                    "task_count": 1,
                    "task_definition_arn": f"${{aws_ecs_task_definition.{task_def_key}.arn}}",
                    "launch_type": "FARGATE",
                    "network_configuration": {
                        "subnets": env.private_subnets,
                        "security_groups": security_group_ids(service.networks),
                        "assign_public_ip": False,
                    },
                },
            )
        )


def _eventbridge_expression(schedule: Schedule) -> str:
    """Render a cloud-neutral schedule as an EventBridge schedule expression.

    EventBridge cron takes six fields rather than the standard five (it adds a
    year), and requires exactly one of day-of-month and day-of-week to be the
    '?' placeholder rather than '*'.
    """
    from composey.models.semantic import RateSchedule

    if isinstance(schedule, RateSchedule):
        # rate(1 hour) is singular, rate(2 hours) is plural.
        unit = schedule.unit if schedule.value != 1 else schedule.unit.rstrip("s")
        return f"rate({schedule.value} {unit})"

    # Cron schedule
    minute, hour, day_of_month, month, day_of_week = schedule.expression.split()

    if day_of_week == "*":
        day_of_week = "?"
    elif day_of_month == "*":
        day_of_month = "?"
    else:
        raise ScheduleError(
            f"schedule {schedule.expression!r} constrains both day-of-month and "
            f"day-of-week, which EventBridge cannot express: it requires one of "
            f"them to be unset."
        )

    return f"cron({minute} {hour} {day_of_month} {month} {day_of_week} *)"
