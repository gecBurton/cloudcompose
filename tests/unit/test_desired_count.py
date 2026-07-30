"""How many tasks a service starts with, and who owns that number afterwards.

`desired_count` was hardcoded to one and never read `min_scale`, so a service
asking for a floor of two deployed one task, autoscaling raised it back to two,
and the next `terraform apply` reset it to one again. The stack never converged,
and every deploy briefly halved the capacity the application had asked for.
"""

import pytest

from composey.compiler.inference import infer
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application, Service


def _env() -> AwsEnvironment:
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:eu-west-2:1:cluster/c",
    )


def _service(**scale) -> dict:
    app = Application(
        name="shop", services=[Service(name="web", image="nginx", **scale)]
    )
    return infer(app, _env()).aws_ecs_service["web_service"].model_dump()


def test_a_service_starts_at_the_floor_it_asked_for():
    assert _service(min_scale=3, max_scale=10)["desired_count"] == 3


def test_one_instance_is_the_default():
    assert _service()["desired_count"] == 1


def test_autoscaling_takes_ownership_of_the_count():
    # Once a scaling policy moves the count, terraform must stop asserting it,
    # or each apply undoes the last scaling activity.
    lifecycle = _service(min_scale=2, max_scale=10)["lifecycle"]

    assert lifecycle["ignore_changes"] == ["desired_count"]


def test_an_unscaled_service_keeps_its_count_declared():
    # Nothing else is going to change it, so terraform reconciling drift here is
    # the behaviour that is wanted.
    assert _service(min_scale=2, max_scale=1)["lifecycle"] is None


@pytest.mark.parametrize("min_scale,max_scale", [(1, 1), (4, 4)])
def test_the_floor_and_the_count_never_disagree(min_scale, max_scale):
    service = _service(min_scale=min_scale, max_scale=max_scale)

    assert service["desired_count"] == min_scale
