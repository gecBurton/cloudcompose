"""
A scheduled service becomes a Container Apps Job, not a Container App.

A Container App is always-on: a nightly task deployed as one runs
continuously, and one that exits when its work is done is restarted
indefinitely. A Job runs to completion on its trigger and stops, which is what
a schedule asks for.
"""

import json

import pytest

from composey.compiler import compile_to_terraform
from composey.compiler.inference.azure import infer
from composey.exceptions import ScheduleError
from composey.models.environment import AzureEnvironment
from composey.models.semantic import (
    Application,
    CronSchedule,
    RateSchedule,
    Service,
)


def _env() -> AzureEnvironment:
    return AzureEnvironment(
        name="prod",
        region="uksouth",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )


def _app(schedule) -> Application:
    return Application(
        name="myapp",
        services=[
            Service(
                name="cleanup",
                image="busybox",
                capability="container",
                command=["echo", "tidying"],
                schedule=schedule,
            )
        ],
    )


class TestScheduledServicesBecomeJobs:
    def test_a_job_is_created(self):
        resources = infer(_app(CronSchedule(expression="0 2 * * *")), _env())

        job = resources.azurerm_container_app_job["cleanup"]
        assert job.schedule_trigger_config == [{"cron_expression": "0 2 * * *"}]

    def test_no_always_on_container_app_is_created(self):
        """The bug this replaces: the task ran continuously."""
        resources = infer(_app(CronSchedule(expression="0 2 * * *")), _env())

        assert "cleanup" not in resources.azurerm_container_app

    def test_unscheduled_services_are_still_container_apps(self):
        app = Application(
            name="myapp",
            services=[Service(name="web", image="nginx", capability="container")],
        )
        resources = infer(app, _env())

        assert "web" in resources.azurerm_container_app
        assert not resources.azurerm_container_app_job

    def test_the_job_runs_in_the_platform_environment(self):
        resources = infer(_app(CronSchedule(expression="0 2 * * *")), _env())
        job = resources.azurerm_container_app_job["cleanup"]

        assert job.container_app_environment_id == (
            "${data.azurerm_container_app_environment.main.id}"
        )

    def test_the_container_spec_matches_a_normal_service(self):
        """
        Jobs and apps share one container-spec builder, so a scheduled task
        gets the same image resolution and command handling.
        """
        resources = infer(_app(CronSchedule(expression="0 2 * * *")), _env())
        container = resources.azurerm_container_app_job["cleanup"].template[0][
            "container"
        ][0]

        assert container["image"] == "busybox"
        assert container["args"] == ["echo", "tidying"]


class TestRateSchedules:
    """
    Azure has no rate dialect, so an interval is rendered as the cron that
    means the same thing — or rejected if cron cannot say it.
    """

    @pytest.mark.parametrize(
        "value,unit,expected",
        [
            (1, "minutes", "* * * * *"),
            (5, "minutes", "*/5 * * * *"),
            (30, "minutes", "*/30 * * * *"),
            (1, "hours", "0 * * * *"),
            (6, "hours", "0 */6 * * *"),
            (1, "days", "0 0 * * *"),
        ],
    )
    def test_intervals_cron_can_express(self, value, unit, expected):
        resources = infer(_app(RateSchedule(value=value, unit=unit)), _env())
        job = resources.azurerm_container_app_job["cleanup"]

        assert job.schedule_trigger_config == [{"cron_expression": expected}]

    @pytest.mark.parametrize(
        "value,unit",
        [(7, "minutes"), (7, "hours"), (2, "days")],
    )
    def test_intervals_cron_cannot_express_are_rejected(self, value, unit):
        """
        Every 7 hours would silently become "0 */7 * * *", which fires at 00:00,
        07:00, 14:00, 21:00 and then again 3 hours later — not every 7 hours.
        """
        with pytest.raises(ScheduleError):
            infer(_app(RateSchedule(value=value, unit=unit)), _env())


def test_compiled_output_emits_the_job(tmp_path):
    compose = tmp_path / "compose.yml"
    compose.write_text(
        "services:\n"
        "  cleanup:\n"
        "    image: busybox\n"
        "    command: ['echo', 'hi']\n"
        "    x-composey:\n"
        "      schedule: '0 3 * * *'\n"
    )

    parsed = json.loads(compile_to_terraform(str(compose), _env(), "myapp"))

    job = parsed["resource"]["azurerm_container_app_job"]["cleanup"]
    assert job["schedule_trigger_config"][0]["cron_expression"] == "0 3 * * *"
    assert "azurerm_container_app" not in parsed["resource"]
