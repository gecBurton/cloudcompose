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
