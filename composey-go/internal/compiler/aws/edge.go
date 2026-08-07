package aws

import (
	"fmt"

	"github.com/gecburton/composey/internal/compiler/shared"
	"github.com/gecburton/composey/internal/models"
)

// InferEdgeResources infers CloudFront CDN and WAF resources, mirroring
// _edge.py's infer_edge_resources.
//
// For services with CDN enabled, creates CloudFront distributions with AWS
// WAF protection.
func InferEdgeResources(
	resources *models.AWSResources,
	app *models.Application,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
) {
	for i := range app.Services {
		service := &app.Services[i]
		if !service.CDNEnabled || service.Ingress == nil {
			continue
		}

		// Create WAF Web ACL.
		wafKey := service.Name + "_waf"
		waf := models.NewWafv2WebAcl()
		waf.Name = getName(service.Name + "-waf")
		waf.Scope = "CLOUDFRONT"
		providerRef := shared.CloudFrontProviderRef
		waf.Provider = &providerRef
		waf.VisibilityConfig = map[string]any{
			"cloudwatch_metrics_enabled": true,
			"metric_name":                service.Name + "WAF",
			"sampled_requests_enabled":   true,
		}
		waf.Rule = []map[string]any{
			{
				"name":            "AWS-AWSManagedRulesCommonRuleSet",
				"priority":        1,
				"override_action": map[string]any{"none": map[string]any{}},
				"statement": map[string]any{
					"managed_rule_group_statement": map[string]any{
						"name":        "AWSManagedRulesCommonRuleSet",
						"vendor_name": "AWS",
					},
				},
				"visibility_config": map[string]any{
					"cloudwatch_metrics_enabled": true,
					"metric_name":                "AWSManagedRulesCommonRuleSet",
					"sampled_requests_enabled":   true,
				},
			},
		}
		waf.Tags = tags
		resources.Wafv2WebAcl[wafKey] = waf

		// Create CloudFront distribution.
		cdnKey := service.Name + "_cdn"
		cdn := models.NewCloudfrontDistribution()
		comment := fmt.Sprintf("CDN for %s", service.Name)
		cdn.Comment = &comment
		cdn.Origin = []map[string]any{
			{
				"domain_name": fmt.Sprintf("${data.aws_lb.%s.dns_name}", shared.ALBDataSourceKey),
				"origin_id":   "ALB",
				"custom_origin_config": map[string]any{
					"http_port":              80,
					"https_port":             443,
					"origin_protocol_policy": "http-only",
					"origin_ssl_protocols":   []string{"TLSv1.2"},
				},
			},
		}
		cdn.DefaultCacheBehavior = map[string]any{
			"allowed_methods": []string{
				"DELETE",
				"GET",
				"HEAD",
				"OPTIONS",
				"PATCH",
				"POST",
				"PUT",
			},
			"cached_methods":         []string{"GET", "HEAD"},
			"target_origin_id":       "ALB",
			"viewer_protocol_policy": "redirect-to-https",
			"forwarded_values": map[string]any{
				"query_string": true,
				"cookies":      map[string]any{"forward": "all"},
			},
		}
		webAclID := fmt.Sprintf("${aws_wafv2_web_acl.%s.arn}", wafKey)
		cdn.WebAclID = &webAclID
		cdn.Tags = tags
		resources.CloudfrontDistribution[cdnKey] = cdn
	}
}
