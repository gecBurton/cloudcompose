package shared

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Core Normalize() contract: service/relationship extraction, port
// selection, scaling passthrough, and the determinism and missing-image
// fallback guarantees. Split out of a single, larger
// normalizer_contract_test.go into files grouped by
// concern — see volumes_test.go, xcloud_test.go, ingress_test.go,
// platform_settings_test.go, database_name_test.go, schedule_test.go,
// networks_test.go, and build_test.go for the rest.

// --- core contract -----------------------------------------------------

func TestNormalizeBasicService(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Ports: []models.PortConfig{{Target: 80, Published: "80"}},
				Environment: map[string]*string{
					"DEBUG": strPtr("true"),
				},
			},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if result.Name != "myapp" {
		t.Errorf("Name = %q, want %q", result.Name, "myapp")
	}
	if len(result.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(result.Services))
	}
	svc := result.Services[0]
	if svc.Port == nil || *svc.Port != 80 {
		t.Errorf("Port = %v, want 80", svc.Port)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("expected no public services, got %v", result.PublicServices())
	}
	if svc.Env["DEBUG"] != "true" {
		t.Errorf("env[DEBUG] = %q, want %q", svc.Env["DEBUG"], "true")
	}
}

func TestNormalizeRelationships(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:     "nginx",
				DependsOn: map[string]struct{}{"db": {}},
			},
			"db": {Image: "postgres:16"},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if len(result.Relationships) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(result.Relationships))
	}
	rel := result.Relationships[0]
	if rel.Client != "web" || rel.Server != "db" {
		t.Errorf("relationship = %+v, want client=web server=db", rel)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("depends_on must not make anything public, got %v", result.PublicServices())
	}
}

func TestNormalizeNoPublicServiceWithoutPorts(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"worker": {Image: "myapp/worker"},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if result.Services[0].Port != nil {
		t.Errorf("Port = %v, want nil", result.Services[0].Port)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("expected no public services, got %v", result.PublicServices())
	}
}

func TestNormalizeMultiplePortsTakesFirst(t *testing.T) {
	t.Parallel()
	// The target of the *first declared* port, not the published one — and
	// not the smallest, largest, or any other port in the list.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Ports: []models.PortConfig{
					{Target: 3000, Published: "80"},
					{Target: 9000, Published: "9000"},
				},
			},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if result.Services[0].Port == nil || *result.Services[0].Port != 3000 {
		t.Errorf("Port = %v, want 3000", result.Services[0].Port)
	}
}

func TestNormalizeScalingPassthrough(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:  "nginx",
				XCloud: map[string]interface{}{"min_scale": 2, "max_scale": 10},
			},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if result.Services[0].MinScale != 2 {
		t.Errorf("MinScale = %d, want 2", result.Services[0].MinScale)
	}
	if result.Services[0].MaxScale != 10 {
		t.Errorf("MaxScale = %d, want 10", result.Services[0].MaxScale)
	}
}

func TestNormalizeExplicitMinScaleZeroIsPreserved(t *testing.T) {
	t.Parallel()
	// min_scale: 0 (scale-to-zero) is a legitimate, validated value — the
	// model allows it explicitly (min_scale >= 0, only max_scale requires
	// >= 1). A redundant defaulting pass in Normalize used to treat any
	// zero MinScale as "unset" and silently reset it to 1, discarding an
	// explicit min_scale: 0 for any service that had an x-cloud block
	// at all (confirmed against a real x-cloud block 2026-08-06).
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:  "nginx",
				XCloud: map[string]interface{}{"min_scale": 0, "max_scale": 3},
			},
			"api": {Image: "nginx"},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	byName := map[string]models.Service{}
	for _, s := range result.Services {
		byName[s.Name] = s
	}

	if byName["web"].MinScale != 0 {
		t.Errorf("web MinScale = %d, want 0 (explicitly set)", byName["web"].MinScale)
	}
	if byName["web"].MaxScale != 3 {
		t.Errorf("web MaxScale = %d, want 3", byName["web"].MaxScale)
	}
	// A service with no x-cloud block at all must still default
	// correctly — this is the case the fix's default has to keep serving.
	if byName["api"].MinScale != 1 {
		t.Errorf("api MinScale = %d, want 1 (default, no x-cloud block)", byName["api"].MinScale)
	}
	if byName["api"].MaxScale != 1 {
		t.Errorf("api MaxScale = %d, want 1 (default, no x-cloud block)", byName["api"].MaxScale)
	}
}

func TestNormalizeMissingImageFallsBackToPlaceholder(t *testing.T) {
	t.Parallel()
	// A missing image must not crash the compiler — it degrades to a
	// placeholder rather than failing normalization outright.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].Image != "placeholder" {
		t.Errorf("Image = %q, want %q", result.Services[0].Image, "placeholder")
	}
}

func TestNormalizeServiceOrderIsDeterministic(t *testing.T) {
	t.Parallel()
	// Go map iteration order is randomized per run; Normalize must not leak
	// that into its own output. Confirmed nondeterministic before this test
	// existed: five runs of the equivalent compose file through the real
	// CLI produced five different service orderings (2026-08-06).
	names := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}
	services := make(map[string]models.ComposeService, len(names))
	for _, n := range names {
		services[n] = models.ComposeService{Image: "nginx"}
	}
	app := &models.ComposeApplication{Services: services}

	var first []string
	for i := 0; i < 10; i++ {
		result, err := Normalize(app, "myapp")
		if err != nil {
			t.Fatalf("Normalize failed: %v", err)
		}
		got := serviceNames(result.Services)
		if i == 0 {
			first = got
			continue
		}
		if !slicesEqual(first, got) {
			t.Fatalf("run %d order = %v, want %v (same as run 0)", i, got, first)
		}
	}
}

func TestNormalizeRelationshipOrderIsDeterministic(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				DependsOn: map[string]struct{}{
					"cache": {}, "db": {}, "queue": {},
				},
			},
			"cache": {Image: "redis:7"},
			"db":    {Image: "postgres:16"},
			"queue": {Image: "rabbitmq"},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	var servers []string
	for _, r := range result.Relationships {
		if r.Client == "web" {
			servers = append(servers, r.Server)
		}
	}
	want := []string{"cache", "db", "queue"}
	if !slicesEqual(servers, want) {
		t.Errorf("relationship order = %v, want %v", servers, want)
	}
}
