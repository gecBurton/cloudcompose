package shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFindComposeFile_PrefersCanonicalNameOverLegacy confirms the
// precedence order matches `docker compose`'s own: compose.yaml/
// compose.yml before the legacy docker-compose.yaml/docker-compose.yml
// names, and .yaml before .yml within each pair.
func TestFindComposeFile_PrefersCanonicalNameOverLegacy(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("services: {}\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got, err := FindComposeFile(dir)
	if err != nil {
		t.Fatalf("FindComposeFile: %v", err)
	}
	want := filepath.Join(dir, "compose.yml")
	if got != want {
		t.Errorf("FindComposeFile = %s, want %s", got, want)
	}
}

// TestFindComposeFile_FallsBackToLegacyName confirms a directory with
// only the legacy docker-compose.yml name still resolves, for projects
// that haven't renamed their compose file to the newer compose.yaml/
// compose.yml convention.
func TestFindComposeFile_FallsBackToLegacyName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0644); err != nil {
		t.Fatalf("write docker-compose.yml: %v", err)
	}

	got, err := FindComposeFile(dir)
	if err != nil {
		t.Fatalf("FindComposeFile: %v", err)
	}
	want := filepath.Join(dir, "docker-compose.yml")
	if got != want {
		t.Errorf("FindComposeFile = %s, want %s", got, want)
	}
}

// TestFindComposeFile_NoneFoundReturnsHelpfulError confirms an empty
// directory produces an error naming every filename that was tried,
// rather than a bare "not found" -- this is the message a user sees
// when they run cloudcompose with no -f in a directory with no compose
// file at all.
func TestFindComposeFile_NoneFoundReturnsHelpfulError(t *testing.T) {
	dir := t.TempDir()

	_, err := FindComposeFile(dir)
	if err == nil {
		t.Fatal("expected an error when no compose file exists")
	}
	for _, name := range ComposeFileCandidates {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("expected error to mention %s, got: %v", name, err)
		}
	}
}
