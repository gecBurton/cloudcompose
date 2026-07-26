import json

from composey.compiler.generator import generate
from composey.compiler.inference import infer
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application, Ingress, Service

ALB_ARN = "arn:aws:lb:us-east-1:123456789012:loadbalancer/app/shared-alb/123"


def _env(region: str = "us-east-1") -> AwsEnvironment:
    return AwsEnvironment(
        name="prod",
        region=region,
        vpc_id="vpc-123",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster",
        alb_arn=ALB_ARN,
        alb_listener_arn="arn:aws:lb:us-east-1:123456789012:listener/app/shared-alb/123/456",
        alb_security_group_id="sg-alb0123456789",
    )


def _app(cdn_enabled: bool) -> Application:
    return Application(
        name="site",
        services=[
            Service(
                name="web",
                image="nginx",
                port=80,
                cdn_enabled=cdn_enabled,
                ingress=Ingress(),
            )
        ],
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


def test_cloudfront_waf_is_created_in_us_east_1_from_another_region():
    # A CLOUDFRONT-scoped web ACL only exists in us-east-1, so deploying an
    # application elsewhere must still create the ACL through an aliased
    # us-east-1 provider rather than the environment's own region.
    env = _env(region="eu-west-2")
    manifest = json.loads(generate(infer(_app(cdn_enabled=True), env), env))

    assert manifest["provider"]["aws"] == [
        {"region": "eu-west-2"},
        {"region": "us-east-1", "alias": "us_east_1"},
    ]

    waf = manifest["resource"]["aws_wafv2_web_acl"]["web_waf"]
    assert waf["scope"] == "CLOUDFRONT"
    assert waf["provider"] == "aws.us_east_1"

    # Everything else stays in the environment's region on the default provider.
    assert "provider" not in manifest["resource"]["aws_ecs_service"]["web_service"]


def test_no_aliased_provider_without_cdn():
    env = _env(region="eu-west-2")
    manifest = json.loads(generate(infer(_app(cdn_enabled=False), env), env))

    assert manifest["provider"]["aws"] == {"region": "eu-west-2"}
