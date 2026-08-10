package shared

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// --- grace period key aliasing -----------------------------------------------

func TestStartupGracePeriodIsRead(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {Image: "nginx", XCloud: map[string]interface{}{"startup_grace_period": 120}},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].StartupGracePeriod == nil || *result.Services[0].StartupGracePeriod != 120 {
		t.Errorf("StartupGracePeriod = %v, want 120", result.Services[0].StartupGracePeriod)
	}
}

func TestDeprecatedHealthCheckGracePeriodStillWorks(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {Image: "nginx", XCloud: map[string]interface{}{"health_check_grace_period": 90}},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].StartupGracePeriod == nil || *result.Services[0].StartupGracePeriod != 90 {
		t.Errorf("StartupGracePeriod = %v, want 90 (from the deprecated key)", result.Services[0].StartupGracePeriod)
	}
}

func TestNeutralNameWinsWhenBothAreGiven(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				XCloud: map[string]interface{}{
					"startup_grace_period":      120,
					"health_check_grace_period": 90,
				},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].StartupGracePeriod == nil || *result.Services[0].StartupGracePeriod != 120 {
		t.Errorf("StartupGracePeriod = %v, want 120 (the neutral name wins)", result.Services[0].StartupGracePeriod)
	}
}
