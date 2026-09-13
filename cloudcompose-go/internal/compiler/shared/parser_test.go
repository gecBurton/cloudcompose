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

// TestParseCompose_RequiresTopLevelName confirms a compose file with no
// top-level `name:` is rejected outright, not silently defaulted.
func TestParseCompose_RequiresTopLevelName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeComposeFile(t, dir, "services:\n  web:\n    image: nginx\n")

	_, err := ParseCompose(path)
	if err == nil {
		t.Fatal("expected an error for a compose file with no top-level `name:`")
	}
}

// TestParseCompose_ReadsTopLevelName confirms ComposeApplication.Name
// comes from the file's own `name:`, not its path.
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

// TestParseCompose_RejectsNameContainingSlash confirms `name:` is
// validated the same way as an environment's own name -- it's used
// verbatim in a backend state key.
func TestParseCompose_RejectsNameContainingSlash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeComposeFile(t, dir, "name: prod/apps\nservices:\n  web:\n    image: nginx\n")

	_, err := ParseCompose(path)
	if err == nil {
		t.Fatal("expected an error for a `name:` containing '/'")
	}
}

// TestParseCompose_AcceptsSafeNames confirms ordinary names pass.
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

// TestParseCompose_ReadsTopLevelXCloud confirms a top-level `x-cloud:`
// block round-trips through the real compose-go loader into
// ComposeApplication.XCloud, decodable via AppSettingsFor.
func TestParseCompose_ReadsTopLevelXCloud(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeComposeFile(t, dir, "name: test\nx-cloud:\n  azure:\n    subnet_index: 2\nservices:\n  web:\n    image: nginx\n")

	app, err := ParseCompose(path)
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	settings, err := AppSettingsFor(app)
	if err != nil {
		t.Fatalf("AppSettingsFor failed: %v", err)
	}
	if settings.Azure == nil || settings.Azure.SubnetIndex == nil || *settings.Azure.SubnetIndex != 2 {
		t.Errorf("expected Azure.SubnetIndex = 2, got %+v", settings.Azure)
	}
}
