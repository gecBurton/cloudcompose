import pytest

from composey.compiler.inference import _eventbridge_expression
from composey.compiler.normalizer import _parse_schedule, normalize
from composey.models.compose import Application as DockerApplication
from composey.models.compose import Service as DockerService
from composey.models.semantic import CronSchedule, RateSchedule


def parse_and_render(raw: str) -> str:
    return _eventbridge_expression(_parse_schedule(raw))


@pytest.mark.parametrize(
    "raw,expression",
    [
        ("0 2 * * *", "0 2 * * *"),
        ("*/5 * * * *", "*/5 * * * *"),
        # AWS spellings are accepted, but the provider dialect is not what gets
        # stored: the wrapper goes, the year field goes, '?' becomes '*'.
        ("cron(0 2 * * ? *)", "0 2 * * *"),
        ("cron(0 2 ? * MON *)", "0 2 * * MON"),
        ("cron(0 2 * * ?)", "0 2 * * *"),
    ],
)
def test_cron_is_stored_cloud_neutrally(raw, expression):
    schedule = _parse_schedule(raw)

    assert isinstance(schedule, CronSchedule)
    assert schedule.expression == expression


@pytest.mark.parametrize(
    "raw,value,unit",
    [
        ("every 1 hour", 1, "hours"),
        ("every hour", 1, "hours"),
        ("every 30 minutes", 30, "minutes"),
        ("every 2 days", 2, "days"),
        ("rate(1 hour)", 1, "hours"),
        ("rate(15 minutes)", 15, "minutes"),
    ],
)
def test_intervals_are_stored_cloud_neutrally(raw, value, unit):
    schedule = _parse_schedule(raw)

    assert isinstance(schedule, RateSchedule)
    assert (schedule.value, schedule.unit) == (value, unit)


@pytest.mark.parametrize(
    "raw", ["", "0 2 * *", "0 2 * * * * *", "hourly", "every fortnight"]
)
def test_unparseable_schedules_are_rejected(raw):
    with pytest.raises(ValueError):
        _parse_schedule(raw)


@pytest.mark.parametrize(
    "raw,rendered",
    [
        # EventBridge needs six fields and a '?' placeholder in whichever of
        # day-of-month / day-of-week is unconstrained.
        ("0 2 * * *", "cron(0 2 * * ? *)"),
        ("0 2 1 * *", "cron(0 2 1 * ? *)"),
        ("0 2 * * MON", "cron(0 2 ? * MON *)"),
        # Singular unit for 1, plural otherwise.
        ("every 1 hour", "rate(1 hour)"),
        ("every 2 hours", "rate(2 hours)"),
        ("every 30 minutes", "rate(30 minutes)"),
    ],
)
def test_eventbridge_rendering(raw, rendered):
    assert parse_and_render(raw) == rendered


@pytest.mark.parametrize(
    "raw", ["cron(0 2 * * ? *)", "cron(0 2 ? * MON *)", "rate(1 hour)", "rate(5 days)"]
)
def test_aws_expressions_round_trip_unchanged(raw):
    # Existing compose files must keep compiling to the same infrastructure;
    # this is what lets the golden snapshots stay byte-identical.
    assert parse_and_render(raw) == raw


def test_schedule_constraining_both_day_fields_is_rejected():
    # Standard cron allows it, EventBridge does not. Fail with an explanation
    # rather than emitting Terraform that AWS will refuse.
    with pytest.raises(ValueError, match="day-of-month and day-of-week"):
        parse_and_render("0 2 1 * MON")


def test_normalizer_parses_schedule_from_compose():
    docker_app = DockerApplication(
        services={
            "cleanup": DockerService(
                image="busybox",
                **{"x-composey": {"schedule": "every 6 hours"}},
            )
        }
    )

    schedule = normalize(docker_app, "test-project").services[0].schedule

    assert schedule == RateSchedule(value=6, unit="hours")
