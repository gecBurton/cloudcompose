package models

import (
	"testing"

	yaml "go.yaml.in/yaml/v4"
)

// backendWrapper mirrors how BackendConfig is always actually
// unmarshalled in production -- nested under a `backend:` field.
type backendWrapper struct {
	Backend BackendConfig `yaml:"backend"`
}

func TestBackendConfig_UnmarshalsLocalBlock(t *testing.T) {
	t.Parallel()
	var w backendWrapper
	if err := yaml.Unmarshal([]byte("backend:\n  local:\n    path: ../state/prod.tfstate\n"), &w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := w.Backend
	if b.Local == nil || b.Local.Path != "../state/prod.tfstate" {
		t.Errorf("expected Local.Path decoded, got %+v", b.Local)
	}
	if b.AWS != nil || b.Azure != nil || b.Gcp != nil {
		t.Errorf("expected every remote provider block nil, got %+v", b)
	}
}

func TestBackendConfig_UnmarshalsAwsBlock(t *testing.T) {
	t.Parallel()
	var w backendWrapper
	yamlSrc := "backend:\n  aws:\n    bucket: my-bucket\n    region: eu-west-2\n"
	if err := yaml.Unmarshal([]byte(yamlSrc), &w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := w.Backend
	if b.Local != nil {
		t.Error("expected Local=nil for an aws: block")
	}
	if b.AWS == nil || b.AWS.Bucket != "my-bucket" || b.AWS.Region != "eu-west-2" {
		t.Errorf("expected AWS.Bucket/Region decoded, got %+v", b.AWS)
	}
}

func TestBackendConfig_RoundTripsLocalBlock(t *testing.T) {
	t.Parallel()
	original := backendWrapper{Backend: BackendConfig{Local: &LocalBackendConfig{Path: "../state/prod.tfstate"}}}
	out, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var roundTripped backendWrapper
	if err := yaml.Unmarshal(out, &roundTripped); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if roundTripped.Backend.Local == nil || *roundTripped.Backend.Local != *original.Backend.Local {
		t.Errorf("round-trip mismatch: got %+v, want %+v", roundTripped.Backend, original.Backend)
	}
}

func TestBackendConfig_RoundTripsAwsBlock(t *testing.T) {
	t.Parallel()
	original := backendWrapper{Backend: BackendConfig{AWS: &AwsBackendConfig{Bucket: "my-bucket", Region: "eu-west-2"}}}
	out, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var roundTripped backendWrapper
	if err := yaml.Unmarshal(out, &roundTripped); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if roundTripped.Backend.Local != nil || roundTripped.Backend.AWS == nil || *roundTripped.Backend.AWS != *original.Backend.AWS {
		t.Errorf("round-trip mismatch: got %+v, want %+v", roundTripped.Backend, original.Backend)
	}
}
