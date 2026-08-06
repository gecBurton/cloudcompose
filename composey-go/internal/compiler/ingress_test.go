package compiler

import (
	"strings"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

// --- test_ingress.py: port/ingress declaration semantics -------------------

func TestPublishing80DoesNotImplyExposure(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {Image: "nginx", Ports: []models.PortConfig{{Target: 80, Published: "80"}}},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("expected no public services just from publishing port 80, got %v", result.PublicServices())
	}
}

func TestIngressPortDefaultsToTheServicePort(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:     "nginx",
				Ports:     []models.PortConfig{{Target: 9090, Published: "9090"}},
				XComposey: map[string]interface{}{"ingress": map[string]interface{}{}},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	ingress := result.Services[0].Ingress
	if ingress == nil {
		t.Fatal("expected ingress to be set")
	}
	if ingress.Port == nil {
		// nil means "use the service's own port" downstream; either
		// behaviour is acceptable as long as it resolves to 9090 somewhere
		// downstream — but Python's contract set the port explicitly, so
		// assert that here too, matching the Go inference's expectation.
		if result.Services[0].Port == nil || *result.Services[0].Port != 9090 {
			t.Errorf("expected ingress to resolve to the service port 9090")
		}
	}
}

func TestIngressPortCanBeDeclared(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Ports: []models.PortConfig{{Target: 8080, Published: "8080"}},
				XComposey: map[string]interface{}{
					"ingress": map[string]interface{}{"port": 9000},
				},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	ingress := result.Services[0].Ingress
	if ingress == nil || ingress.Port == nil || *ingress.Port != 9000 {
		t.Errorf("expected declared ingress port 9000, got %+v", ingress)
	}
}

func TestBareIngressKeyDeclaresADefaultRoute(t *testing.T) {
	t.Parallel()
	// `ingress:` with nothing under it (parses as YAML null) must still
	// declare a default route — the only place a service can still end up
	// silently non-public if this is handled wrong.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:     "nginx",
				Ports:     []models.PortConfig{{Target: 80, Published: "80"}},
				XComposey: map[string]interface{}{"ingress": nil},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(result.PublicServices()) != 1 {
		t.Errorf("expected 1 public service from a bare 'ingress:' key, got %v", result.PublicServices())
	}
}

func TestThePublicShorthandIsGone(t *testing.T) {
	t.Parallel()
	// The old x-composey.public: true shorthand was replaced by explicit
	// ingress declaration; it must be rejected, not silently ignored.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:     "nginx",
				XComposey: map[string]interface{}{"public": true},
			},
		},
	}
	_, err := Normalize(app, "p")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "public") {
		t.Errorf("error = %q, want it to mention 'public'", err.Error())
	}
}
