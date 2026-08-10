package shared

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// --- x-cloud validation --------------------------------------------------

func TestCapabilityCanBeDeclaredExplicitly(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"thing": {
				Image:  "acme/private-thing:1",
				XCloud: map[string]interface{}{"capability": "database"},
			},
		},
	}

	result, err := Normalize(app, "test-project")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].Capability != models.CapabilityDatabase {
		t.Errorf("Capability = %q, want database", result.Services[0].Capability)
	}
}

func TestCapabilityOverrideBeatsInference(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"db": {
				Image:  "postgres:16",
				XCloud: map[string]interface{}{"capability": "container"},
			},
		},
	}

	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].Capability != models.CapabilityContainer {
		t.Errorf("Capability = %q, want container (explicit override)", result.Services[0].Capability)
	}
}

func TestUnknownCapabilityIsRejectedByName(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"thing": {
				Image:  "acme/private-thing:1",
				XCloud: map[string]interface{}{"capability": "databse"},
			},
		},
	}

	_, err := Normalize(app, "test-project")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "service 'thing' has an invalid x-cloud") {
		t.Errorf("error = %q, want it to mention service 'thing' has an invalid x-cloud", err.Error())
	}
}

func TestMisspelledKeyIsRejectedRatherThanIgnored(t *testing.T) {
	t.Parallel()
	// The failure this validation exists for: `capabilty` was silently
	// dropped, and the service deployed as whatever the compiler guessed.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"thing": {
				Image:  "acme/private-thing:1",
				XCloud: map[string]interface{}{"capabilty": "database"},
			},
		},
	}

	_, err := Normalize(app, "test-project")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "capabilty") {
		t.Errorf("error = %q, want it to mention the misspelled key 'capabilty'", err.Error())
	}
}

func TestMisspelledPublicIsRejected(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"thing": {
				Image:  "acme/private-thing:1",
				XCloud: map[string]interface{}{"publik": true},
			},
		},
	}

	_, err := Normalize(app, "test-project")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "publik") {
		t.Errorf("error = %q, want it to mention 'publik'", err.Error())
	}
}

func TestOutOfRangeValuesAreRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		setting map[string]interface{}
	}{
		{"size", map[string]interface{}{"size": "enormous"}},
		{"min_scale", map[string]interface{}{"min_scale": -1}},
		{"max_scale", map[string]interface{}{"max_scale": 0}},
		{"cpu", map[string]interface{}{"cpu": 0}},
		{"startup_grace_period", map[string]interface{}{"startup_grace_period": -5}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app := &models.ComposeApplication{
				Services: map[string]models.ComposeService{
					"thing": {Image: "acme/private-thing:1", XCloud: tc.setting},
				},
			}
			_, err := Normalize(app, "test-project")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), "invalid x-cloud") {
				t.Errorf("error = %q, want it to mention 'invalid x-cloud'", err.Error())
			}
		})
	}
}

func publicTestApp(frontendXC, backendXC map[string]interface{}) *models.ComposeApplication {
	return &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"frontend": {
				Image:  "frontend",
				Ports:  []models.PortConfig{{Target: 8081, Published: "8081"}},
				XCloud: frontendXC,
			},
			"backend": {
				Image:  "backend",
				Ports:  []models.PortConfig{{Target: 8080, Published: "8080"}},
				XCloud: backendXC,
			},
		},
	}
}

func TestNoPublicServiceIsDetectedFromNonStandardPorts(t *testing.T) {
	t.Parallel()
	// The behaviour that left two real applications deployed but unreachable.
	result, err := Normalize(publicTestApp(nil, nil), "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("expected no public services, got %v", result.PublicServices())
	}
}

func TestPublicCanBeDeclaredExplicitly(t *testing.T) {
	t.Parallel()
	result, err := Normalize(
		publicTestApp(map[string]interface{}{"ingress": map[string]interface{}{}}, nil), "p",
	)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	names := serviceNames(result.PublicServices())
	if len(names) != 1 || names[0] != "frontend" {
		t.Errorf("public services = %v, want [frontend]", names)
	}
}

func TestTwoServicesMayBothBePublicOnDistinctPaths(t *testing.T) {
	t.Parallel()
	result, err := Normalize(
		publicTestApp(
			map[string]interface{}{"ingress": map[string]interface{}{"path": "/"}},
			map[string]interface{}{"ingress": map[string]interface{}{"path": "/api"}},
		),
		"p",
	)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	names := serviceNames(result.PublicServices())
	if len(names) != 2 {
		t.Errorf("expected 2 public services, got %v", names)
	}
}

func TestTwoServicesOnTheSamePathAreRejected(t *testing.T) {
	t.Parallel()
	_, err := Normalize(
		publicTestApp(
			map[string]interface{}{"ingress": map[string]interface{}{}},
			map[string]interface{}{"ingress": map[string]interface{}{}},
		),
		"p",
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "both serve") {
		t.Errorf("error = %q, want it to mention 'both serve'", err.Error())
	}
}
