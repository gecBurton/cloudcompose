package azure

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for docs/azure-aws-parity-todo.md's health-check/probe item:
// Container Apps' liveness_probe/startup_probe built from a service's
// ingress health check and StartupGracePeriod.

func intPtr(n int) *int { return &n }

func TestHealthProbesAzure_DefaultsToHTTPRootPath(t *testing.T) {
	t.Parallel()
	service := &models.Service{
		Name: "web",
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/"},
		},
	}
	liveness, startup, err := healthProbesAzure(service)
	if err != nil {
		t.Fatalf("healthProbesAzure failed: %v", err)
	}
	if liveness == nil {
		t.Fatalf("expected a liveness probe")
	}
	if liveness.Transport != "HTTP" || liveness.Port != 80 || liveness.Path != "/" {
		t.Errorf("liveness = %+v, want {Transport: HTTP, Port: 80, Path: /}", liveness)
	}
	if startup != nil {
		t.Errorf("expected no startup probe when StartupGracePeriod is unset, got %+v", startup)
	}
}

func TestHealthProbesAzure_UsesIngressPortOverServicePort(t *testing.T) {
	t.Parallel()
	service := &models.Service{
		Name: "web",
		Port: intPtr(3000),
		Ingress: &models.Ingress{
			Path:        "/",
			Port:        intPtr(8080),
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/health"},
		},
	}
	liveness, _, err := healthProbesAzure(service)
	if err != nil {
		t.Fatalf("healthProbesAzure failed: %v", err)
	}
	if liveness.Port != 8080 {
		t.Errorf("liveness.Port = %d, want 8080 (ingress port should win over service port)", liveness.Port)
	}
	if liveness.Path != "/health" {
		t.Errorf("liveness.Path = %q, want /health", liveness.Path)
	}
}

func TestHealthProbesAzure_FallsBackToServicePort(t *testing.T) {
	t.Parallel()
	service := &models.Service{
		Name: "web",
		Port: intPtr(3000),
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/"},
		},
	}
	liveness, _, err := healthProbesAzure(service)
	if err != nil {
		t.Fatalf("healthProbesAzure failed: %v", err)
	}
	if liveness.Port != 3000 {
		t.Errorf("liveness.Port = %d, want 3000 (service port used when ingress.port is unset)", liveness.Port)
	}
}

func TestHealthProbesAzure_TCPHealthCheckOmitsPath(t *testing.T) {
	t.Parallel()
	service := &models.Service{
		Name: "tcp-svc",
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeTCP, Path: "/"},
		},
	}
	liveness, _, err := healthProbesAzure(service)
	if err != nil {
		t.Fatalf("healthProbesAzure failed: %v", err)
	}
	if liveness.Transport != "TCP" {
		t.Errorf("liveness.Transport = %q, want TCP", liveness.Transport)
	}
	if liveness.Path != "" {
		t.Errorf("liveness.Path = %q, want empty (path is meaningless for TCP transport)", liveness.Path)
	}
}

func TestHealthProbesAzure_StartupGracePeriodBudget(t *testing.T) {
	t.Parallel()
	// The exact case docs/spikes/azure/doctor.tf already prototyped:
	// 120s becomes 12 failures at a 10s interval.
	service := &models.Service{
		Name:               "doctor",
		StartupGracePeriod: intPtr(120),
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/"},
		},
	}
	_, startup, err := healthProbesAzure(service)
	if err != nil {
		t.Fatalf("healthProbesAzure failed: %v", err)
	}
	if startup == nil {
		t.Fatalf("expected a startup probe")
	}
	if startup.IntervalSeconds != 10 {
		t.Errorf("startup.IntervalSeconds = %d, want 10", startup.IntervalSeconds)
	}
	if startup.FailureCountThreshold != 12 {
		t.Errorf("startup.FailureCountThreshold = %d, want 12", startup.FailureCountThreshold)
	}
}

func TestHealthProbesAzure_StartupGracePeriodRoundsUp(t *testing.T) {
	t.Parallel()
	// 95s does not divide evenly by 10 -- must round UP to 10 failures
	// (100s of budget), not down to 9 (90s), since rounding down would
	// under-cover the window the user actually asked for.
	service := &models.Service{
		Name:               "web",
		StartupGracePeriod: intPtr(95),
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/"},
		},
	}
	_, startup, err := healthProbesAzure(service)
	if err != nil {
		t.Fatalf("healthProbesAzure failed: %v", err)
	}
	if startup.FailureCountThreshold != 10 {
		t.Errorf("startup.FailureCountThreshold = %d, want 10 (rounded up from 9.5)", startup.FailureCountThreshold)
	}
}

func TestHealthProbesAzure_ZeroOrNilGracePeriodOmitsStartupProbe(t *testing.T) {
	t.Parallel()
	for _, gp := range []*int{nil, intPtr(0)} {
		service := &models.Service{
			Name:               "web",
			StartupGracePeriod: gp,
			Ingress: &models.Ingress{
				Path:        "/",
				HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/"},
			},
		}
		_, startup, err := healthProbesAzure(service)
		if err != nil {
			t.Fatalf("healthProbesAzure failed: %v", err)
		}
		if startup != nil {
			t.Errorf("StartupGracePeriod=%v: expected no startup probe, got %+v", gp, startup)
		}
	}
}

func TestHealthProbesAzure_RejectsGracePeriodBeyondBudget(t *testing.T) {
	t.Parallel()
	// 240 failures * 10s interval = 2400s (40 min) is the largest window
	// this mapping can express; one second beyond that must be rejected
	// outright, not silently truncated to a shorter window than asked.
	service := &models.Service{
		Name:               "web",
		StartupGracePeriod: intPtr(2401),
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/"},
		},
	}
	_, _, err := healthProbesAzure(service)
	if err == nil {
		t.Fatalf("expected an error for a startup_grace_period beyond what the probe budget can express")
	}
}

func TestHealthProbesAzure_AcceptsGracePeriodAtExactBudgetLimit(t *testing.T) {
	t.Parallel()
	service := &models.Service{
		Name:               "web",
		StartupGracePeriod: intPtr(2400),
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/"},
		},
	}
	_, startup, err := healthProbesAzure(service)
	if err != nil {
		t.Fatalf("healthProbesAzure failed at the exact budget limit: %v", err)
	}
	if startup.FailureCountThreshold != azureProbeMaxFailureCount {
		t.Errorf("FailureCountThreshold = %d, want %d", startup.FailureCountThreshold, azureProbeMaxFailureCount)
	}
}

func TestContainerSpecAzure_OnlyAppliesProbesToIngressServices(t *testing.T) {
	t.Parallel()
	// A worker/background service (no Ingress) should get no probes at
	// all -- there's no ALB-equivalent health check for it on AWS
	// either, so nothing to mirror.
	app := &models.Application{Name: "app"}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	service := &models.Service{Name: "worker", Image: "worker:latest", Size: models.ServiceSizeSmall}
	container, _, err := containerSpecAzure(service, app, &env, resources, nil, nil, testGetNameAzure, nil, "")
	if err != nil {
		t.Fatalf("containerSpecAzure failed: %v", err)
	}
	if container.LivenessProbe != nil || container.StartupProbe != nil {
		t.Errorf("expected no probes for a service with no Ingress, got liveness=%+v startup=%+v", container.LivenessProbe, container.StartupProbe)
	}
}

// TestContainerSpecAzure_IngressServiceGetsSingleElementProbeSlices
// checks the real integration point healthProbesAzure's own unit tests
// can't cover: ContainerAppContainer.LivenessProbe/StartupProbe are
// []ContainerAppProbe, not *ContainerAppProbe -- confirmed against the
// real schema (`go run ./cmd/schema-check`) that both are
// `nesting_mode: list` with no `max_items` cap, so a bare-struct field
// would have been the same class of bug schema-check's own doc comment
// warns about. This checks containerSpecAzure actually wraps
// healthProbesAzure's single pointer into a one-element slice, not just
// that the slice type compiles.
func TestContainerSpecAzure_IngressServiceGetsSingleElementProbeSlices(t *testing.T) {
	t.Parallel()
	app := &models.Application{Name: "app"}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	service := &models.Service{
		Name:               "web",
		Image:              "web:latest",
		Size:               models.ServiceSizeSmall,
		StartupGracePeriod: intPtr(30),
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/health"},
		},
	}
	container, _, err := containerSpecAzure(service, app, &env, resources, nil, nil, testGetNameAzure, nil, "")
	if err != nil {
		t.Fatalf("containerSpecAzure failed: %v", err)
	}
	if len(container.LivenessProbe) != 1 {
		t.Fatalf("LivenessProbe = %+v, want exactly one entry", container.LivenessProbe)
	}
	if container.LivenessProbe[0].Path != "/health" {
		t.Errorf("LivenessProbe[0].Path = %q, want /health", container.LivenessProbe[0].Path)
	}
	if len(container.StartupProbe) != 1 {
		t.Fatalf("StartupProbe = %+v, want exactly one entry (StartupGracePeriod was set)", container.StartupProbe)
	}
	if container.StartupProbe[0].FailureCountThreshold != 3 {
		t.Errorf("StartupProbe[0].FailureCountThreshold = %d, want 3 (30s / 10s interval)", container.StartupProbe[0].FailureCountThreshold)
	}
}
