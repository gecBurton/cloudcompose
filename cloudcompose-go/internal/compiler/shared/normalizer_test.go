package shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

func TestNormalizeFlaskExample(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../../examples/flask/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}

	semanticApp, err := Normalize(composeApp, "testapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	// Verify basic structure
	if semanticApp.Name != "testapp" {
		t.Errorf("Expected name 'testapp', got '%s'", semanticApp.Name)
	}

	if len(semanticApp.Services) != 3 {
		t.Errorf("Expected 3 services, got %d", len(semanticApp.Services))
	}

	// Find and verify database service
	var dbService *models.Service
	for i := range semanticApp.Services {
		if semanticApp.Services[i].Name == "db" {
			dbService = &semanticApp.Services[i]
			break
		}
	}

	if dbService == nil {
		t.Fatal("Database service not found")
	}

	if dbService.Capability != models.CapabilityDatabase {
		t.Errorf("Expected capability 'database', got '%s'", dbService.Capability)
	}

	if dbService.DatabaseName == nil || *dbService.DatabaseName != "example" {
		t.Errorf("Expected database_name 'example', got %v", dbService.DatabaseName)
	}

	if dbService.Image != "mariadb:10.6.4-focal" {
		t.Errorf("Expected image 'mariadb:10.6.4-focal', got '%s'", dbService.Image)
	}

	// Verify relationships
	if len(semanticApp.Relationships) != 2 {
		t.Errorf("Expected 2 relationships, got %d", len(semanticApp.Relationships))
	}
}

func TestNormalizeAllExamples(t *testing.T) {
	t.Parallel()
	examples := []string{
		"../../../../examples/flask/compose.yml",
		"../../../../examples/flask-redis/compose.yml",
		"../../../../examples/minio-s3/compose.yml",
		"../../../../examples/hello/compose.yml",
		"../../../../examples/scaling/compose.yml",
	}

	for _, example := range examples {
		t.Run(example, func(t *testing.T) {
			t.Parallel()
			composeApp, err := ParseCompose(example)
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}

			semanticApp, err := Normalize(composeApp, "testapp")
			if err != nil {
				t.Fatalf("Normalize failed: %v", err)
			}

			if semanticApp.Name != "testapp" {
				t.Errorf("Expected name 'testapp', got '%s'", semanticApp.Name)
			}

			if len(semanticApp.Services) == 0 {
				t.Error("Expected at least one service")
			}

			// Validate all services
			for i := range semanticApp.Services {
				if err := semanticApp.Services[i].Validate(); err != nil {
					t.Errorf("Service validation failed: %v", err)
				}
			}
		})
	}
}

// TestParseAndNormalizeRejectsNamedVolumesFromRealComposeFiles exercises the
// actual boundary between compose-go and this package's own types, not a
// hand-built models.ComposeApplication. A prior version of NamedVolumeSource
// type-switched on interface{} expecting either a bare string or
// models.VolumeDefinition, and every real compose file's volume entries
// arrive as compose-go's own types.ServiceVolumeConfig instead — matching
// neither case, so named-volume rejection silently did nothing against any
// real file while every hand-built-struct test still passed (confirmed
// against real compose files, both short- and long-form syntax).
// This test would have caught that: it is the only one in this package that
// goes through ParseCompose for a volume-bearing file at all.
func TestParseAndNormalizeRejectsNamedVolumesFromRealComposeFiles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		compose string
	}{
		{
			name: "short-form named volume",
			compose: `
services:
  web:
    image: nginx
    volumes:
      - db-data:/data
volumes:
  db-data: {}
`,
		},
		{
			name: "long-form named volume",
			compose: `
services:
  web:
    image: nginx
    volumes:
      - type: volume
        source: db-data
        target: /data
volumes:
  db-data: {}
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "compose.yml")
			if err := os.WriteFile(path, []byte(tc.compose), 0o644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			composeApp, err := ParseCompose(path)
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}

			_, err = Normalize(composeApp, "myapp")
			if err == nil {
				t.Fatal("expected Normalize to reject the named volume, got nil")
			}
			if !strings.Contains(err.Error(), "mounts named volume(s) db-data") {
				t.Errorf("error = %q, want it to mention the named volume", err.Error())
			}
		})
	}
}

// TestParseAndNormalizeAcceptsLocalOnlyMountsFromRealComposeFiles is the
// real-file counterpart to TestLocalOnlyMountsAreIgnored — same assertion,
// but through ParseCompose rather than a hand-built VolumeDefinition, for
// the same reason given above.
func TestParseAndNormalizeAcceptsLocalOnlyMountsFromRealComposeFiles(t *testing.T) {
	t.Parallel()
	compose := `
services:
  web:
    image: nginx
    volumes:
      - ./local:/data
      - /abs/path:/data2:ro
      - anon-vol
`
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(path, []byte(compose), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	composeApp, err := ParseCompose(path)
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}

	if _, err := Normalize(composeApp, "myapp"); err != nil {
		t.Errorf("expected no error for local-only mounts, got: %v", err)
	}
}
