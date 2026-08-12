package azure

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for docs/azure-aws-parity-todo.md's Priority 4 "New gap found"
// item: Azure Container Apps' Consumption plan requires CPU and memory
// to be an exact matched pair from a fixed table, not just independently
// under the 2vCPU/4GiB cap.

func TestAzureCPUMemoryPairAzure_RejectsMismatchedPair(t *testing.T) {
	t.Parallel()
	// 1.0 vCPU only pairs validly with 2.0Gi -- this is exactly the
	// compute-tuning example's worker service (size: medium = 1.0 vCPU,
	// with an explicit memory: 4096 override = 4Gi), which is why that
	// example has no Azure golden fixture: this is the correct behavior,
	// not a bug to fix in the fixture.
	err := azureCPUMemoryPairAzure("worker", 1.0, 4.0)
	if err == nil {
		t.Fatalf("expected an error for 1.0 vCPU + 4Gi (not a valid Consumption pair)")
	}
	if !strings.Contains(err.Error(), "worker") {
		t.Errorf("error should name the service, got: %v", err)
	}
}

func TestAzureCPUMemoryPairAzure_RejectsOffStepCPU(t *testing.T) {
	t.Parallel()
	// Consumption CPU must land on a 0.25 vCPU step; 0.3 doesn't.
	err := azureCPUMemoryPairAzure("web", 0.3, 0.6)
	if err == nil {
		t.Fatalf("expected an error for CPU not on a 0.25 vCPU step")
	}
}

func TestAzureCPUMemoryPairAzure_AllowsEveryDocumentedPair(t *testing.T) {
	t.Parallel()
	// Every pair Microsoft's own vCPU/memory allocation table lists,
	// confirmed against learn.microsoft.com/azure/container-apps/containers
	// (not guessed): 0.25 vCPU steps from 0.25 up to 4.0, each paired
	// with exactly 2x that many GiB. Only the pairs within this
	// project's own 2vCPU/4GiB Consumption-only ceiling
	// (azureConsumptionMaxCPU/azureConsumptionMaxMemoryGB) are relevant
	// here, since getCPUCoresAzure/getMemoryGBAzure already reject
	// anything above that ceiling before azureCPUMemoryPairAzure ever
	// sees it -- but the pairing check itself has no opinion on the
	// ceiling, so this covers the full documented table for
	// completeness.
	pairs := []struct {
		cpu, memoryGB float64
	}{
		{0.25, 0.5},
		{0.5, 1.0},
		{0.75, 1.5},
		{1.0, 2.0},
		{1.25, 2.5},
		{1.5, 3.0},
		{1.75, 3.5},
		{2.0, 4.0},
	}
	for _, p := range pairs {
		if err := azureCPUMemoryPairAzure("web", p.cpu, p.memoryGB); err != nil {
			t.Errorf("expected %g vCPU + %gGi to be a valid pair, got error: %v", p.cpu, p.memoryGB, err)
		}
	}
}

func TestResolveContainerResourcesAzure_RejectsMismatchedCpuMemoryPair(t *testing.T) {
	t.Parallel()
	// The real, end-to-end shape of the bug: a size default (medium =
	// 1.0 vCPU) combined with an independent memory: override. Neither
	// getCPUCoresAzure nor getMemoryGBAzure alone can catch this --
	// each only checks its own value against the 2vCPU/4GiB ceiling,
	// not against the other's resolved value -- which is exactly why
	// resolveContainerResourcesAzure exists as the one place that
	// validates the pair together.
	mem := 4096
	service := &models.Service{Name: "worker", Size: models.ServiceSizeMedium, Memory: &mem}
	_, _, err := resolveContainerResourcesAzure(service)
	if err == nil {
		t.Fatalf("expected an error for size: medium (1.0 vCPU) + memory: 4096 (4Gi), not a valid Consumption pair")
	}
}

func TestResolveContainerResourcesAzure_AllowsMatchedExplicitOverrides(t *testing.T) {
	t.Parallel()
	// The compute-tuning example's other service (api): explicit
	// cpu: 1024 (1.0 vCPU) + memory: 2048 (2Gi) -- a valid pair set
	// entirely independently of size:, which should still work.
	cpu := 1024
	mem := 2048
	service := &models.Service{Name: "api", CPU: &cpu, Memory: &mem}
	gotCPU, gotMemory, err := resolveContainerResourcesAzure(service)
	if err != nil {
		t.Fatalf("resolveContainerResourcesAzure failed: %v", err)
	}
	if gotCPU != 1.0 {
		t.Errorf("cpu = %v, want 1.0", gotCPU)
	}
	if gotMemory != "2048Mi" {
		t.Errorf("memory = %v, want 2048Mi", gotMemory)
	}
}

func TestResolveContainerResourcesAzure_AllowsSizeDefaultsAlone(t *testing.T) {
	t.Parallel()
	// Every plain size: default must itself be a valid pair, since
	// shared.SizeMappings' three sizes all happen to satisfy Memory ==
	// 2x CPU (512=2x256, 2048=2x1024, 8192=2x4096) -- this is a
	// regression test for that property holding, not just a smoke test.
	for _, size := range []models.ServiceSize{models.ServiceSizeSmall, models.ServiceSizeMedium} {
		service := &models.Service{Name: "web", Size: size}
		if _, _, err := resolveContainerResourcesAzure(service); err != nil {
			t.Errorf("size %q should resolve to a valid pair, got error: %v", size, err)
		}
	}
}

func TestMemoryGBFromContainerAppsString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want float64
	}{
		{"512Mi", 0.5},
		{"2048Mi", 2.0},
		{"4Gi", 4.0},
		{"0.5Gi", 0.5},
	}
	for _, c := range cases {
		got, err := memoryGBFromContainerAppsString(c.in)
		if err != nil {
			t.Errorf("memoryGBFromContainerAppsString(%q) failed: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("memoryGBFromContainerAppsString(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMemoryGBFromContainerAppsString_RejectsUnknownSuffix(t *testing.T) {
	t.Parallel()
	if _, err := memoryGBFromContainerAppsString("2048"); err == nil {
		t.Fatalf("expected an error for a memory string with no Mi/Gi suffix")
	}
}

func TestInferContainerApps_DefaultAutoScalingWhenMaxScaleAboveOne(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, MinScale: 1, MaxScale: 3},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()

	inferContainerApps(resources, app, &env, minimalGetName("prod", "myapp"), nil, "", "", nil, nil, nil)

	containerApp, ok := resources.ContainerApp["web"]
	if !ok {
		t.Fatalf("expected a Container App for web")
	}
	rules := containerApp.Template.CustomScaleRule
	if len(rules) != 2 {
		t.Fatalf("expected 2 custom scale rules (cpu, memory), got %d: %+v", len(rules), rules)
	}
	byType := map[string]models.ContainerAppCustomScaleRule{}
	for _, r := range rules {
		byType[r.CustomRuleType] = r
	}
	cpu, ok := byType["cpu"]
	if !ok {
		t.Fatalf("expected a cpu scale rule, got %+v", rules)
	}
	if cpu.Metadata["type"] != "Utilization" || cpu.Metadata["value"] != "70" {
		t.Errorf("cpu rule metadata = %v, want type=Utilization value=70", cpu.Metadata)
	}
	mem, ok := byType["memory"]
	if !ok {
		t.Fatalf("expected a memory scale rule, got %+v", rules)
	}
	if mem.Metadata["type"] != "Utilization" || mem.Metadata["value"] != "80" {
		t.Errorf("memory rule metadata = %v, want type=Utilization value=80", mem.Metadata)
	}
}

func TestInferContainerApps_NoDefaultAutoScalingWhenMaxScaleIsOne(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, MinScale: 1, MaxScale: 1},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()

	inferContainerApps(resources, app, &env, minimalGetName("prod", "myapp"), nil, "", "", nil, nil, nil)

	containerApp := resources.ContainerApp["web"]
	if len(containerApp.Template.CustomScaleRule) != 0 {
		t.Errorf("expected no custom scale rules when max_scale=1, got %+v", containerApp.Template.CustomScaleRule)
	}
}

func TestInferContainerApps_ExplicitAutoScalingOverridesDefault(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{
				Name: "web", Capability: models.CapabilityContainer, MinScale: 1, MaxScale: 3,
				AutoScaling: &models.AutoScalingConfig{
					Metrics: []models.AutoScalingMetric{
						{Type: models.AutoScalingMetricCPU, TargetValue: 50},
					},
				},
			},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()

	inferContainerApps(resources, app, &env, minimalGetName("prod", "myapp"), nil, "", "", nil, nil, nil)

	containerApp := resources.ContainerApp["web"]
	rules := containerApp.Template.CustomScaleRule
	if len(rules) != 1 {
		t.Fatalf("expected exactly the explicitly-declared cpu rule (no memory default added), got %d: %+v", len(rules), rules)
	}
	if rules[0].Metadata["value"] != "50" {
		t.Errorf("expected the explicit target value (50) to be used, not the default (70), got %v", rules[0].Metadata)
	}
}

func TestInferContainerApps_RequestsPerTargetMetricStillUsesHttpScaleRule(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{
				Name: "web", Capability: models.CapabilityContainer, MinScale: 1, MaxScale: 3,
				AutoScaling: &models.AutoScalingConfig{
					Metrics: []models.AutoScalingMetric{
						{Type: models.AutoScalingMetricRequestsPerTarget, TargetValue: 200},
					},
				},
			},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()

	inferContainerApps(resources, app, &env, minimalGetName("prod", "myapp"), nil, "", "", nil, nil, nil)

	containerApp := resources.ContainerApp["web"]
	if len(containerApp.Template.CustomScaleRule) != 0 {
		t.Errorf("requests_per_target should not produce a custom_scale_rule, got %+v", containerApp.Template.CustomScaleRule)
	}
	if len(containerApp.Template.HTTPScaleRule) != 1 || containerApp.Template.HTTPScaleRule[0].ConcurrentRequests != "200" {
		t.Errorf("expected 1 http_scale_rule with concurrent_requests=200, got %+v", containerApp.Template.HTTPScaleRule)
	}
}

// Tests for docs/azure-aws-parity-todo.md's Priority 3 Redis private
// networking item (added 2026-08-08).

// Tests for docs/azure-aws-parity-todo.md's Priority 4 size-ceiling
// item (added 2026-08-08).

func TestGetCPUCoresAzure_RejectsSizeAboveConsumptionCap(t *testing.T) {
	t.Parallel()
	// "large" now derives from shared.SizeMappings (4096 CPU units =
	// 4.0 vCPU), which exceeds Container Apps' Consumption tier limit
	// of 2 vCPU per container -- this is exactly what the "scaling"
	// example's web service hits, which is why it was removed from
	// azureGoldenExamples rather than golden-tested (there's no valid
	// Azure output to compare against; the correct behavior is a
	// rejection, not a value).
	service := &models.Service{Name: "web", Size: models.ServiceSizeLarge}
	_, err := getCPUCoresAzure(service)
	if err == nil {
		t.Fatalf("expected an error for size: large (4 vCPU > 2 vCPU cap)")
	}
}

func TestGetCPUCoresAzure_RejectsExplicitCPUAboveConsumptionCap(t *testing.T) {
	t.Parallel()
	cpu := 3072 // 3.0 vCPU
	service := &models.Service{Name: "web", CPU: &cpu}
	_, err := getCPUCoresAzure(service)
	if err == nil {
		t.Fatalf("expected an error for an explicit cpu: override above the 2 vCPU cap")
	}
}

func TestGetCPUCoresAzure_AllowsSizesWithinCap(t *testing.T) {
	t.Parallel()
	for _, size := range []models.ServiceSize{models.ServiceSizeSmall, models.ServiceSizeMedium} {
		service := &models.Service{Name: "web", Size: size}
		cores, err := getCPUCoresAzure(service)
		if err != nil {
			t.Errorf("size %q should be within the cap, got error: %v", size, err)
		}
		if cores <= 0 {
			t.Errorf("size %q returned non-positive cores: %v", size, cores)
		}
	}
}

func TestGetMemoryGBAzure_RejectsSizeAboveConsumptionCap(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web", Size: models.ServiceSizeLarge}
	_, err := getMemoryGBAzure(service)
	if err == nil {
		t.Fatalf("expected an error for size: large (8Gi > 4Gi cap)")
	}
}

func TestGetMemoryGBAzure_RejectsExplicitMemoryAboveConsumptionCap(t *testing.T) {
	t.Parallel()
	mem := 5120 // 5Gi
	service := &models.Service{Name: "web", Memory: &mem}
	_, err := getMemoryGBAzure(service)
	if err == nil {
		t.Fatalf("expected an error for an explicit memory: override above the 4Gi cap")
	}
}

func TestGetCPUCoresAzure_MediumMatchesAwsSizeMappings(t *testing.T) {
	t.Parallel()
	// Regression test for the real, already-drifted duplicate found
	// while consolidating the size table (2026-08-08): Azure's own
	// table previously defined medium as 0.5 vCPU where AWS's medium
	// (shared.SizeMappings) is 1.0 vCPU.
	service := &models.Service{Name: "web", Size: models.ServiceSizeMedium}
	cores, err := getCPUCoresAzure(service)
	if err != nil {
		t.Fatalf("getCPUCoresAzure failed: %v", err)
	}
	if cores != 1.0 {
		t.Errorf("medium CPU = %v, want 1.0 (matching shared.SizeMappings[\"medium\"].CPU / 1024)", cores)
	}
}

// Tests for docs/azure-aws-parity-todo.md's health-check/probe item:
// Container Apps' liveness_probe/startup_probe built from a service's
// ingress health check and StartupGracePeriod.

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
