package shared

import (
	"strings"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

// --- test_volumes.py: named volumes are refused, mounts are not ------------

func TestNamedVolumeIsRefused(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Volumes: []models.VolumeDefinition{
					{Type: "volume", Source: "db-data"},
				},
			},
		},
	}

	_, err := Normalize(app, "myapp")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "mounts named volume(s) db-data") {
		t.Errorf("error = %q, want it to mention 'mounts named volume(s) db-data'", err.Error())
	}
	if !strings.Contains(err.Error(), "minio") {
		t.Errorf("error = %q, want it to suggest minio as the alternative", err.Error())
	}
}

func TestEveryNamedVolumeIsListed(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Volumes: []models.VolumeDefinition{
					{Type: "volume", Source: "media"},
					{Type: "volume", Source: "assets"},
				},
			},
		},
	}

	_, err := Normalize(app, "myapp")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "assets, media") {
		t.Errorf("error = %q, want both volumes listed sorted", err.Error())
	}
}

func TestNamedVolumeOnSubstitutedServiceIsAccepted(t *testing.T) {
	t.Parallel()
	// A managed database brings its own storage; the named volume is moot,
	// not an error, once the service is substituted for a managed service.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"db": {
				Image: "postgres:16",
				Volumes: []models.VolumeDefinition{
					{Type: "volume", Source: "db-data"},
				},
			},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.Services[0].Capability != models.CapabilityDatabase {
		t.Errorf("Capability = %q, want database", result.Services[0].Capability)
	}
}

func TestLocalOnlyMountsAreIgnored(t *testing.T) {
	t.Parallel()
	// Shapes below match what compose-go's loader actually produces for
	// each compose-file syntax (confirmed against a real compose file
	// 2026-08-06) — compose-go normalizes every form, short or long, bind
	// or anonymous volume, into this one struct before parser.go ever sees
	// it. There is no code path where a bare "./local:/data" string form
	// reaches ComposeService.Volumes in production, so testing against one
	// would not have caught the bug this file's history records.
	cases := []struct {
		name   string
		volume models.VolumeDefinition
	}{
		{"relative bind", models.VolumeDefinition{Type: "bind", Source: "./local"}},
		{"absolute bind", models.VolumeDefinition{Type: "bind", Source: "/etc/hosts"}},
		{"home-relative bind", models.VolumeDefinition{Type: "bind", Source: "~/config"}},
		{"anonymous volume", models.VolumeDefinition{Type: "volume"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app := &models.ComposeApplication{
				Services: map[string]models.ComposeService{
					"web": {Image: "nginx", Volumes: []models.VolumeDefinition{tc.volume}},
				},
			}
			if _, err := Normalize(app, "myapp"); err != nil {
				t.Errorf("expected no error for a local-only mount, got: %v", err)
			}
		})
	}
}

func TestBindMountAlongsideNamedVolumeStillFails(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Volumes: []models.VolumeDefinition{
					{Type: "bind", Source: "./local"},
					{Type: "volume", Source: "media"},
				},
			},
		},
	}

	_, err := Normalize(app, "myapp")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "media") {
		t.Errorf("error = %q, want it to mention the named volume 'media'", err.Error())
	}
}
