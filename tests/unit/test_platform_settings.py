"""Settings that belong to the platform team rather than the application.

Restored after the Go port deleted normalizer.py (0244d4a). The grace-period
key-aliasing tests (startup_grace_period vs the deprecated
health_check_grace_period) moved to
composey-go/internal/compiler/normalizer_contract_test.go, since normalize()
no longer exists here to drive them. What's left is log-retention and ECS
wiring, unaffected Python inference behavior.
"""

import json

from composey.compiler.generator import generate
from composey.compiler.inference import infer
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application, Service


def _env(**overrides) -> AwsEnvironment:
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-123",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster",
        **overrides,
    )


def _log_groups(env):
    app = Application(name="site", services=[Service(name="web", image="nginx")])
    manifest = json.loads(generate(infer(app, env), env))
    return manifest["resource"]["aws_cloudwatch_log_group"]


def test_log_retention_defaults_to_a_week():
    assert _log_groups(_env())["web_lg"]["retention_in_days"] == 7


def test_log_retention_is_set_by_the_environment():
    # Retention is a platform policy, so it comes from the environment file and
    # is not something an application can choose.
    assert _log_groups(_env(log_retention_days=90))["web_lg"]["retention_in_days"] == 90


def test_grace_period_reaches_the_ecs_service():
    app = Application(
        name="site",
        services=[Service(name="web", image="nginx", startup_grace_period=120)],
    )
    env = _env()
    manifest = json.loads(generate(infer(app, env), env))

    service = manifest["resource"]["aws_ecs_service"]["web_service"]
    assert service["health_check_grace_period_seconds"] == 120
