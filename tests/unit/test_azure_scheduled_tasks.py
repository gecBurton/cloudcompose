"""
Scheduled tasks are not supported on Azure.

AWS routes a service with a schedule to EventBridge and explicitly does not run
it as a persistent ECS service. Azure has no equivalent path, so the schedule
is dropped: a task written to run nightly is deployed as an always-on Container
App, and one that exits when its work is done is restarted forever.

That is a materially different deployment from the one the compose file asked
for, so it must not happen quietly.
"""

import warnings

import pytest

from composey.compiler.inference.azure import infer
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application, CronSchedule, Service


def _env() -> AzureEnvironment:
    return AzureEnvironment(
        name="prod",
        region="uksouth",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )


def _app_with_schedule() -> Application:
    return Application(
        name="myapp",
        services=[
            Service(
                name="cleanup",
                image="busybox",
                capability="container",
                schedule=CronSchedule(expression="0 2 * * *"),
            )
        ],
    )


def test_schedule_is_not_dropped_silently():
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        infer(_app_with_schedule(), _env())

    messages = [str(c.message) for c in caught]
    assert any("Scheduled tasks are not supported on Azure" in m for m in messages)
    assert any("cleanup" in m for m in messages), "the warning must name the service"


def test_unscheduled_services_do_not_warn():
    app = Application(
        name="myapp",
        services=[Service(name="web", image="nginx", capability="container")],
    )

    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        infer(app, _env())

    assert not [c for c in caught if "Scheduled tasks" in str(c.message)]


@pytest.mark.filterwarnings("ignore:Scheduled tasks")
def test_the_service_is_still_deployed():
    """
    Warning, not skipping: the workload still runs, just continuously rather
    than on its schedule. Dropping it entirely would be a different decision.
    """
    resources = infer(_app_with_schedule(), _env())
    assert "cleanup" in resources.azurerm_container_app
