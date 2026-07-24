import json

from composey.compiler.generator import generate
from composey.compiler.inference import infer
from composey.models.environment import Environment
from composey.models.semantic import Application, Service

ALB_ARN = "arn:aws:lb:us-east-1:123456789012:loadbalancer/app/shared-alb/123"


def _env() -> Environment:
    return Environment(
        name="prod",
        vpc_id="vpc-123",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster",
        alb_arn=ALB_ARN,
        alb_listener_arn="arn:aws:lb:us-east-1:123456789012:listener/app/shared-alb/123/456",
    )


def _app(cdn_enabled: bool) -> Application:
    return Application(
        name="site",
        services=[Service(name="web", image="nginx", port=80, cdn_enabled=cdn_enabled)],
        public_service="web",
    )


def test_cdn_origin_uses_alb_dns_data_source():
    env = _env()
    manifest = json.loads(generate(infer(_app(cdn_enabled=True), env), env))

    origin = manifest["resource"]["aws_cloudfront_distribution"]["web_cdn"]["origin"][0]
    assert origin["domain_name"] == "${data.aws_lb.shared_alb.dns_name}"

    # The referenced data source must exist and be keyed off the ALB ARN.
    assert manifest["data"]["aws_lb"] == {"shared_alb": {"arn": ALB_ARN}}


def test_no_alb_data_source_without_cdn():
    env = _env()
    manifest = json.loads(generate(infer(_app(cdn_enabled=False), env), env))

    assert "aws_lb" not in manifest.get("data", {})
