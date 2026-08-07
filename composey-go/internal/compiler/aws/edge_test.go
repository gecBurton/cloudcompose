package aws

import (
	"github.com/gecburton/composey/internal/compiler/shared"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

// TestInferEdgeResources_RealProductionStackExample exercises CDN+WAF
// inference through the real parser/normalizer boundary using the
// production-stack example, the only golden example with cdn: true.
func TestInferEdgeResources_RealProductionStackExample(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/production-stack/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "production-stack")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	getName := minimalGetName("prod", "production-stack")

	resources := models.NewAWSResources()
	InferEdgeResources(resources, app, &env, getName, nil)

	waf, ok := resources.Wafv2WebAcl["web_waf"]
	if !ok {
		t.Fatalf("expected a WAF ACL for the CDN-enabled service, got keys %v", keysOf(resources.Wafv2WebAcl))
	}
	if waf.Scope != "CLOUDFRONT" {
		t.Errorf("Scope = %q, want CLOUDFRONT", waf.Scope)
	}
	if waf.Provider == nil || *waf.Provider != shared.CloudFrontProviderRef {
		t.Errorf("Provider = %v, want %q", waf.Provider, shared.CloudFrontProviderRef)
	}

	cdn, ok := resources.CloudfrontDistribution["web_cdn"]
	if !ok {
		t.Fatalf("expected a CloudFront distribution, got keys %v", keysOf(resources.CloudfrontDistribution))
	}
	if cdn.WebAclID == nil || *cdn.WebAclID != "${aws_wafv2_web_acl.web_waf.arn}" {
		t.Errorf("WebAclID = %v, want ${aws_wafv2_web_acl.web_waf.arn}", cdn.WebAclID)
	}
	if len(cdn.Origin) != 1 {
		t.Fatalf("expected exactly 1 origin, got %d", len(cdn.Origin))
	}
}

// TestInferEdgeResources_SkipsServicesWithoutCDNOrIngress checks that
// neither a private CDN-enabled service (no ingress) nor a public
// non-CDN service produces edge resources.
func TestInferEdgeResources_SkipsServicesWithoutCDNOrIngress(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "private-cdn", Capability: models.CapabilityContainer, CDNEnabled: true}, // no ingress
			{Name: "public-no-cdn", Capability: models.CapabilityContainer, Ingress: &models.Ingress{Path: "/"}},
		},
	}
	env := fullMockProdEnv()
	resources := models.NewAWSResources()
	InferEdgeResources(resources, app, &env, minimalGetName("prod", "app"), nil)

	if len(resources.Wafv2WebAcl) != 0 {
		t.Errorf("expected no WAF ACLs, got %v", keysOf(resources.Wafv2WebAcl))
	}
	if len(resources.CloudfrontDistribution) != 0 {
		t.Errorf("expected no CloudFront distributions, got %v", keysOf(resources.CloudfrontDistribution))
	}
}
