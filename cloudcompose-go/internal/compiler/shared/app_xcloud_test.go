package shared

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// --- top-level x-cloud validation (app-wide, not per-service) ------------

func TestAppSettingsFor_NoXCloudReturnsZeroValue(t *testing.T) {
	t.Parallel()
	settings, err := AppSettingsFor(&models.ComposeApplication{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if settings.Azure != nil {
		t.Errorf("expected Azure to be nil with no x-cloud, got %+v", settings.Azure)
	}
}

func TestAppSettingsFor_DecodesAzureSubnetIndex(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		XCloud: map[string]interface{}{
			"azure": map[string]interface{}{"subnet_index": 3},
		},
	}
	settings, err := AppSettingsFor(app)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if settings.Azure == nil || settings.Azure.SubnetIndex == nil || *settings.Azure.SubnetIndex != 3 {
		t.Errorf("expected Azure.SubnetIndex = 3, got %+v", settings.Azure)
	}
}

func TestAppSettingsFor_RejectsUnknownTopLevelKey(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		XCloud: map[string]interface{}{"azur": map[string]interface{}{"subnet_index": 0}},
	}
	_, err := AppSettingsFor(app)
	if err == nil {
		t.Fatal("expected an error for a misspelled top-level key, got nil")
	}
	if !strings.Contains(err.Error(), "azur") {
		t.Errorf("error = %q, want it to mention the misspelled key 'azur'", err.Error())
	}
}

func TestAppSettingsFor_RejectsUnknownAzureKey(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		XCloud: map[string]interface{}{"azure": map[string]interface{}{"subnet_indx": 0}},
	}
	_, err := AppSettingsFor(app)
	if err == nil {
		t.Fatal("expected an error for a misspelled azure key, got nil")
	}
	if !strings.Contains(err.Error(), "subnet_indx") {
		t.Errorf("error = %q, want it to mention the misspelled key 'subnet_indx'", err.Error())
	}
}
