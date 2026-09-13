package shared

import (
	"os"
	"path/filepath"
	"testing"
)

// writeComposeFile writes content to <dir>/compose.yml and returns its
// path, for tests that need a real file on disk (ParseCompose reads
// the file directly to recover its own top-level `name:`, see
// composeFileName).
func writeComposeFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	return path
}

// TestParseCompose_RequiresTopLevelName is the regression test for the
// core identity decision in docs/deployment-identity-design.md: an
// application's durable identity is its compose file's own top-level
// `name:` field, not a directory basename, -p/--project flag, or
// COMPOSE_PROJECT_NAME -- none of which survive deleting and
// regenerating artifacts elsewhere. A compose file with no `name:`
// must be rejected outright, not silently defaulted.
func TestParseCompose_RequiresTopLevelName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeComposeFile(t, dir, "services:\n  web:\n    image: nginx\n")

	_, err := ParseCompose(path)
	if err == nil {
		t.Fatal("expected an error for a compose file with no top-level `name:`")
	}
}

// TestParseCompose_ReadsTopLevelName confirms the resolved
// ComposeApplication.Name comes from the file's own `name:`, not
// anything derived from the file's path.
func TestParseCompose_ReadsTopLevelName(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "some-other-directory-name")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := writeComposeFile(t, dir, "name: checkout-api\nservices:\n  web:\n    image: nginx\n")

	app, err := ParseCompose(path)
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	if app.Name != "checkout-api" {
		t.Errorf("app.Name = %q, want %q (directory is named differently, on purpose)", app.Name, "checkout-api")
	}
}

// TestParseCompose_RejectsNameContainingSlash confirms the same
// backend-key-collision check applied to environment.yaml's `name:`
// (initconfig.TestLoad_RejectsNameContainingSlash) and to backend
// naming generally (ValidateBackendName's own tests) also applies
// here: this name is used verbatim to build BackendKeyForApp, so an
// unsanitized value could collide with another app's key.
func TestParseCompose_RejectsNameContainingSlash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeComposeFile(t, dir, "name: prod/apps\nservices:\n  web:\n    image: nginx\n")

	_, err := ParseCompose(path)
	if err == nil {
		t.Fatal("expected an error for a `name:` containing '/'")
	}
}

// TestParseCompose_AcceptsSafeNames confirms ordinary project names are
// unaffected by the new validation.
func TestParseCompose_AcceptsSafeNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"checkout-api", "web_api", "hello"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := writeComposeFile(t, dir, "name: "+name+"\nservices:\n  web:\n    image: nginx\n")

			app, err := ParseCompose(path)
			if err != nil {
				t.Fatalf("ParseCompose failed for name %q: %v", name, err)
			}
			if app.Name != name {
				t.Errorf("app.Name = %q, want %q", app.Name, name)
			}
		})
	}
}
