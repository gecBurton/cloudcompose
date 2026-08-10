package shared

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// --- build context extraction ------------------------------------------

func TestNormalizeExtractsBuildContext(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Build: &models.BuildConfig{Context: "app"},
				Ports: []models.PortConfig{{Target: 80, Published: "80"}},
			},
		},
	}
	result, err := Normalize(app, "prod")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].BuildContext == nil || *result.Services[0].BuildContext != "app" {
		t.Errorf("BuildContext = %v, want \"app\"", result.Services[0].BuildContext)
	}
}
