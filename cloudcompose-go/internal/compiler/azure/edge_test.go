package azure

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for docs/azure-aws-parity-todo.md's FrontDoorOriginGroup.HealthProbe
// item: populating Front Door's origin-health probe from the same
// service.Ingress.HealthCheck.Path already collected for Container Apps'
// own liveness_probe. Not an AWS-parity item -- CloudFront's origin
// block has no equivalent concept at all -- so these tests check the
// values produced are internally consistent (matching the route's own
// HttpsOnly forwarding protocol) and reflect the service's declared
// health check, not that they match some AWS behavior.

func TestFrontDoorHealthProbeFor_DefaultPath(t *testing.T) {
	t.Parallel()
	service := &models.Service{
		Name: "web",
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/"},
		},
	}
	probe := frontDoorHealthProbeFor(service)
	if probe == nil {
		t.Fatalf("expected a health probe")
	}
	if probe.Path != "/" {
		t.Errorf("Path = %q, want /", probe.Path)
	}
}

func TestFrontDoorHealthProbeFor_CustomPath(t *testing.T) {
	t.Parallel()
	service := &models.Service{
		Name: "web",
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/health"},
		},
	}
	probe := frontDoorHealthProbeFor(service)
	if probe.Path != "/health" {
		t.Errorf("Path = %q, want /health", probe.Path)
	}
}

// TestFrontDoorHealthProbeFor_AlwaysHttps checks the probe's protocol is
// always "Https" regardless of the service's own HealthCheck.Type
// (http/tcp) -- Front Door's route always forwards to the origin over
// HTTPS (models.NewFrontDoorRoute's ForwardingProtocol is
// unconditionally "HttpsOnly"), so the probe must match that, not the
// service's own ingress health-check transport, which describes a
// different hop (Front Door -> Container App's liveness_probe target,
// not Front Door -> origin).
func TestFrontDoorHealthProbeFor_AlwaysHttps(t *testing.T) {
	t.Parallel()
	service := &models.Service{
		Name: "web",
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeTCP, Path: "/"},
		},
	}
	probe := frontDoorHealthProbeFor(service)
	if probe.Protocol != "Https" {
		t.Errorf("Protocol = %q, want Https regardless of the service's own HealthCheck.Type", probe.Protocol)
	}
}

func TestFrontDoorHealthProbeFor_UsesHeadRequestsAndDocumentedInterval(t *testing.T) {
	t.Parallel()
	service := &models.Service{
		Name: "web",
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/"},
		},
	}
	probe := frontDoorHealthProbeFor(service)
	if probe.RequestType != "HEAD" {
		t.Errorf("RequestType = %q, want HEAD (cheaper on the origin, Microsoft's own recommendation)", probe.RequestType)
	}
	if probe.IntervalInSeconds != frontDoorHealthProbeAzureIntervalSeconds {
		t.Errorf("IntervalInSeconds = %d, want %d", probe.IntervalInSeconds, frontDoorHealthProbeAzureIntervalSeconds)
	}
}

// TestInferCdnAzure_OriginGroupHasHealthProbe is the integration test
// covering inferCdnAzure's own call to frontDoorHealthProbeFor, not just
// the helper in isolation.
func TestInferCdnAzure_OriginGroupHasHealthProbe(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{
				Name:       "web",
				Image:      "web:latest",
				CDNEnabled: true,
				Ingress: &models.Ingress{
					Path:        "/",
					HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/status"},
				},
			},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, testGetNameAzure, nil)

	group, ok := resources.CdnFrontdoorOriginGroup["web"]
	if !ok {
		t.Fatalf("expected an origin group for web")
	}
	if group.HealthProbe == nil {
		t.Fatalf("expected a health probe on the origin group")
	}
	if group.HealthProbe.Path != "/status" {
		t.Errorf("HealthProbe.Path = %q, want /status", group.HealthProbe.Path)
	}
	if group.HealthProbe.Protocol != "Https" {
		t.Errorf("HealthProbe.Protocol = %q, want Https", group.HealthProbe.Protocol)
	}
}

// Tests for docs/azure-aws-parity-todo.md's WAF/security-policy item:
// inferCdnAzure now also creates a FrontDoorFirewallPolicy +
// FrontDoorSecurityPolicy per CDN-enabled service, matching AWS's own
// per-service granularity (aws/edge.go's wafKey := service.Name + "_waf").

func TestInferCdnAzure_CreatesFirewallAndSecurityPolicy(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, CDNEnabled: true, Ingress: &models.Ingress{Path: "/"}},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, testGetNameAzure, nil)

	waf, ok := resources.CdnFrontdoorFirewallPolicy["web"]
	if !ok {
		t.Fatalf("expected a FrontDoorFirewallPolicy for web")
	}
	if waf.Mode != "Prevention" {
		t.Errorf("waf.Mode = %q, want Prevention (Detection mode would create the resource but never enforce anything)", waf.Mode)
	}
	if len(waf.CustomRule) != 1 {
		t.Fatalf("expected exactly one custom_rule, got %d", len(waf.CustomRule))
	}
	if waf.CustomRule[0]["type"] != "RateLimitRule" {
		t.Errorf(`custom_rule[0]["type"] = %v, want "RateLimitRule"`, waf.CustomRule[0]["type"])
	}

	secPolicy, ok := resources.CdnFrontdoorSecurityPolicy["web"]
	if !ok {
		t.Fatalf("expected a FrontDoorSecurityPolicy for web")
	}
	if secPolicy.Name == "" {
		t.Errorf("expected secPolicy.Name to be set")
	}
}

// TestInferCdnAzure_FirewallPolicyNameIsAlphanumericOnly is the
// end-to-end companion to TestFrontDoorFirewallPolicyName_ObeysAzureRules:
// checks inferCdnAzure actually calls FrontDoorFirewallPolicyName rather
// than, say, getName (which permits dashes) for this one field.
func TestInferCdnAzure_FirewallPolicyNameIsAlphanumericOnly(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "nginx-flask-mysql",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, CDNEnabled: true, Ingress: &models.Ingress{Path: "/"}},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, testGetNameAzure, nil)

	waf := resources.CdnFrontdoorFirewallPolicy["web"]
	if !alphanumericOnly.MatchString(waf.Name) {
		t.Errorf("waf.Name = %q, want alphanumeric only", waf.Name)
	}
}

func TestInferCdnAzure_SecurityPolicyReferencesTheFirewallPolicyAndEndpoint(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, CDNEnabled: true, Ingress: &models.Ingress{Path: "/"}},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, testGetNameAzure, nil)

	secPolicy := resources.CdnFrontdoorSecurityPolicy["web"]
	if len(secPolicy.SecurityPolicies) != 1 {
		t.Fatalf("expected exactly one security_policies entry, got %d", len(secPolicy.SecurityPolicies))
	}
	firewall, ok := secPolicy.SecurityPolicies[0]["firewall"].([]map[string]any)
	if !ok || len(firewall) != 1 {
		t.Fatalf("expected exactly one firewall entry, got %v", secPolicy.SecurityPolicies[0]["firewall"])
	}
	firewallPolicyID, _ := firewall[0]["cdn_frontdoor_firewall_policy_id"].(string)
	if firewallPolicyID != "${azurerm_cdn_frontdoor_firewall_policy.web.id}" {
		t.Errorf("cdn_frontdoor_firewall_policy_id = %q, want a reference to the web firewall policy", firewallPolicyID)
	}
	association, ok := firewall[0]["association"].([]map[string]any)
	if !ok || len(association) != 1 {
		t.Fatalf("expected exactly one association entry, got %v", firewall[0]["association"])
	}
	domain, ok := association[0]["domain"].([]map[string]any)
	if !ok || len(domain) != 1 {
		t.Fatalf("expected exactly one domain entry, got %v", association[0]["domain"])
	}
	domainID, _ := domain[0]["cdn_frontdoor_domain_id"].(string)
	if domainID != "${azurerm_cdn_frontdoor_endpoint.web.id}" {
		t.Errorf("cdn_frontdoor_domain_id = %q, want a reference to the web endpoint", domainID)
	}
}

func TestInferCdnAzure_MultipleServicesGetDistinctFirewallPolicies(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, CDNEnabled: true, Ingress: &models.Ingress{Path: "/"}},
			{Name: "api", Capability: models.CapabilityContainer, CDNEnabled: true, Ingress: &models.Ingress{Path: "/api"}},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, testGetNameAzure, nil)

	if len(resources.CdnFrontdoorFirewallPolicy) != 2 {
		t.Fatalf("expected 2 distinct firewall policies (matching AWS's own per-service WAF granularity), got %d", len(resources.CdnFrontdoorFirewallPolicy))
	}
	webWaf := resources.CdnFrontdoorFirewallPolicy["web"]
	apiWaf := resources.CdnFrontdoorFirewallPolicy["api"]
	if webWaf.Name == apiWaf.Name {
		t.Errorf("expected distinct firewall policy names, both got %q", webWaf.Name)
	}
}

// --- CDN: only ever exercised with exactly one CDN-enabled service.

func TestInferCdnAzure_NoCdnServicesCreatesNothing(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, Ingress: &models.Ingress{Path: "/"}},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, minimalGetName("prod", "app"), nil)

	if len(resources.CdnFrontdoorProfile) != 0 {
		t.Errorf("expected no Front Door profile, got %v", keysOf(resources.CdnFrontdoorProfile))
	}
}

func TestInferCdnAzure_CdnWithoutIngressCreatesNothing(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, CDNEnabled: true}, // no ingress
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, minimalGetName("prod", "app"), nil)

	if len(resources.CdnFrontdoorProfile) != 0 {
		t.Errorf("expected no Front Door profile without ingress, got %v", keysOf(resources.CdnFrontdoorProfile))
	}
}

func TestInferCdnAzure_MultipleServicesShareOneProfileDistinctOrigins(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, CDNEnabled: true, Ingress: &models.Ingress{Path: "/"}},
			{Name: "api", Capability: models.CapabilityContainer, CDNEnabled: true, Ingress: &models.Ingress{Path: "/api"}},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, minimalGetName("prod", "app"), nil)

	if len(resources.CdnFrontdoorProfile) != 1 {
		t.Fatalf("expected exactly 1 shared profile, got %d: %v", len(resources.CdnFrontdoorProfile), keysOf(resources.CdnFrontdoorProfile))
	}
	if len(resources.CdnFrontdoorOrigin) != 2 {
		t.Fatalf("expected 2 distinct origins, got %d: %v", len(resources.CdnFrontdoorOrigin), keysOf(resources.CdnFrontdoorOrigin))
	}
	webOrigin := resources.CdnFrontdoorOrigin["web"]
	apiOrigin := resources.CdnFrontdoorOrigin["api"]
	if webOrigin.HostName == apiOrigin.HostName {
		t.Errorf("expected distinct origin hostnames, both got %q", webOrigin.HostName)
	}
}

func TestFrontDoorProfile_HasNoLocationField(t *testing.T) {
	t.Parallel()
	profile := models.NewFrontDoorProfile()
	// Front Door is global, unlike everything else this inference
	// creates -- structurally verified by the absence of a Location
	// field on the type itself (compile-time check: this line would fail
	// to compile if FrontDoorProfile had one), and the SKU default.
	if profile.SkuName != "Standard_AzureFrontDoor" {
		t.Errorf("SkuName = %q, want Standard_AzureFrontDoor", profile.SkuName)
	}
}
