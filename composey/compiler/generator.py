import json

from ..models.aws import (
    ALB_DATA_SOURCE_KEY,
    CLOUDFRONT_PROVIDER_ALIAS,
    CLOUDFRONT_SCOPE_REGION,
    AWSResources,
)
from ..models.environment import AwsEnvironment
from ..models.terraform import TerraformManifest


def _aws_provider(env: AwsEnvironment, region: str) -> dict:
    """Build an AWS provider configuration for a given region."""
    provider: dict = {"region": region}

    if env.aws_endpoint:
        provider.update(
            {
                "access_key": "test",
                "secret_key": "test",
                "skip_credentials_validation": True,
                "skip_metadata_api_check": True,
                "skip_requesting_account_id": True,
                "s3_use_path_style": True,
                "endpoints": {
                    "s3": env.aws_endpoint,
                    "ecs": env.aws_endpoint,
                    "ec2": env.aws_endpoint,
                    "secretsmanager": env.aws_endpoint,
                    "iam": env.aws_endpoint,
                    "elasticloadbalancing": env.aws_endpoint,
                    "cloudwatch": env.aws_endpoint,
                    "logs": env.aws_endpoint,
                    "wafv2": env.aws_endpoint,
                },
            }
        )

    return provider


def generate(resources: AWSResources, env: AwsEnvironment) -> str:
    # Build provider configuration
    aws_provider = _aws_provider(env, env.region)

    required_providers = {
        "aws": {"source": "hashicorp/aws", "version": "~> 5.0"},
        "random": {"source": "hashicorp/random", "version": "~> 3.6"},
    }
    providers = {"aws": aws_provider}
    data_sources: dict = {}

    # If any service builds from source, wire up the docker provider so it can
    # build images and push to ECR, authenticated via an ECR token data source.
    if resources.docker_image:
        required_providers["docker"] = {
            "source": "kreuzwerker/docker",
            "version": "~> 3.0",
        }
        data_sources["aws_ecr_authorization_token"] = {"token": {}}
        providers["docker"] = {
            "registry_auth": {
                "address": "${data.aws_ecr_authorization_token.token.proxy_endpoint}",
                "username": "${data.aws_ecr_authorization_token.token.user_name}",
                "password": "${data.aws_ecr_authorization_token.token.password}",
            }
        }

    # CloudFront origins reference the shared ALB by DNS name, which the
    # environment only supplies as an ARN. Look it up at apply time.
    if resources.aws_cloudfront_distribution and env.alb_arn:
        data_sources["aws_lb"] = {ALB_DATA_SOURCE_KEY: {"arn": env.alb_arn}}

    # CLOUDFRONT-scoped WAF web ACLs can only be created in us-east-1, so they
    # are pinned to an aliased provider rather than the environment's region.
    if any(acl.scope == "CLOUDFRONT" for acl in resources.aws_wafv2_web_acl.values()):
        edge_provider = _aws_provider(env, CLOUDFRONT_SCOPE_REGION)
        edge_provider["alias"] = CLOUDFRONT_PROVIDER_ALIAS
        providers["aws"] = [aws_provider, edge_provider]

    manifest = TerraformManifest(
        terraform={"required_providers": required_providers},
        provider=providers,
        data=data_sources or None,
        resource=resources,
    )

    # Use model_dump to get a dict, then sort keys for determinism
    manifest_dict = manifest.model_dump(exclude_none=True, by_alias=True)

    # Cleanup: Remove empty resource type dictionaries
    # Terraform JSON fails if a resource type block (like "aws_s3_bucket") is empty.
    if "resource" in manifest_dict:
        manifest_dict["resource"] = {
            k: v for k, v in manifest_dict["resource"].items() if v
        }

    return json.dumps(manifest_dict, indent=2, sort_keys=True)
