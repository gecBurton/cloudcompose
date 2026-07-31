"""Edge services inference (CloudFront, WAF).

Handles inference of CDN and security edge resources.
"""

from typing import Callable

from composey.constants import (
    ALB_DATA_SOURCE_KEY,
    CLOUDFRONT_PROVIDER_REF,
)
from composey.models.aws import (
    AWSResources,
    CloudfrontDistribution,
    Wafv2WebAcl,
)
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application as SemanticApp


def infer_edge_resources(
    resources: AWSResources,
    app: SemanticApp,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> None:
    """Infer CloudFront CDN and WAF resources.

    For services with CDN enabled, creates CloudFront distributions
    with AWS WAF protection.
    """
    for service in app.services:
        if not service.cdn_enabled or not service.ingress:
            continue

        # Create WAF Web ACL
        waf_key = f"{service.name}_waf"
        resources.aws_wafv2_web_acl[waf_key] = Wafv2WebAcl(
            name=get_name(f"{service.name}-waf"),
            scope="CLOUDFRONT",
            provider=CLOUDFRONT_PROVIDER_REF,
            visibility_config={
                "cloudwatch_metrics_enabled": True,
                "metric_name": f"{service.name}WAF",
                "sampled_requests_enabled": True,
            },
            rule=[
                {
                    "name": "AWS-AWSManagedRulesCommonRuleSet",
                    "priority": 1,
                    "override_action": {"none": {}},
                    "statement": {
                        "managed_rule_group_statement": {
                            "name": "AWSManagedRulesCommonRuleSet",
                            "vendor_name": "AWS",
                        }
                    },
                    "visibility_config": {
                        "cloudwatch_metrics_enabled": True,
                        "metric_name": "AWSManagedRulesCommonRuleSet",
                        "sampled_requests_enabled": True,
                    },
                }
            ],
            tags=tags,
        )

        # Create CloudFront distribution
        cdn_key = f"{service.name}_cdn"
        resources.aws_cloudfront_distribution[cdn_key] = CloudfrontDistribution(
            comment=f"CDN for {service.name}",
            origin=[
                {
                    "domain_name": f"${{data.aws_lb.{ALB_DATA_SOURCE_KEY}.dns_name}}",
                    "origin_id": "ALB",
                    "custom_origin_config": {
                        "http_port": 80,
                        "https_port": 443,
                        "origin_protocol_policy": "http-only",
                        "origin_ssl_protocols": ["TLSv1.2"],
                    },
                }
            ],
            default_cache_behavior={
                "allowed_methods": [
                    "DELETE",
                    "GET",
                    "HEAD",
                    "OPTIONS",
                    "PATCH",
                    "POST",
                    "PUT",
                ],
                "cached_methods": ["GET", "HEAD"],
                "target_origin_id": "ALB",
                "viewer_protocol_policy": "redirect-to-https",
                "forwarded_values": {
                    "query_string": True,
                    "cookies": {"forward": "all"},
                },
            },
            web_acl_id=f"${{aws_wafv2_web_acl.{waf_key}.arn}}",
            tags=tags,
        )
