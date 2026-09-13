package models

import (
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v4"
)

// backendWrapper mirrors how BackendConfig is always actually
// unmarshalled in production -- nested under a `backend:` field, not
// as a bare top-level YAML document (the two get different node kinds
// from the underlying yaml.Node for a scalar value).
type backendWrapper struct {
	Backend BackendConfig `yaml:"backend"`
}

func TestBackendConfig_UnmarshalsLocal(t *testing.T) {
	t.Parallel()
	var w backendWrapper
	if err := yaml.Unmarshal([]byte("backend: local"), &w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := w.Backend
	if !b.IsLocal || b.AWS != nil || b.Azure != nil || b.Gcp != nil {
		t.Errorf("expected IsLocal=true and every provider block nil, got %+v", b)
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
	if b.IsLocal {
		t.Error("expected IsLocal=false for an aws: block")
	}
	if b.AWS == nil || b.AWS.Bucket != "my-bucket" || b.AWS.Region != "eu-west-2" {
		t.Errorf("expected AWS.Bucket/Region decoded, got %+v", b.AWS)
	}
}

func TestBackendConfig_RejectsUnsupportedScalar(t *testing.T) {
	t.Parallel()
	var w backendWrapper
	err := yaml.Unmarshal([]byte("backend: aws"), &w)
	if err == nil {
		t.Fatal("expected an error for backend: aws (a bare scalar other than \"local\")")
	}
	if !strings.Contains(err.Error(), "not a supported value") {
		t.Errorf("error = %q, want it to mention 'not a supported value'", err.Error())
	}
}

func TestBackendConfig_MarshalsLocalAsScalar(t *testing.T) {
	t.Parallel()
	out, err := yaml.Marshal(backendWrapper{Backend: BackendConfig{IsLocal: true}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(string(out)) != "backend: local" {
		t.Errorf("Marshal(IsLocal: true) = %q, want %q", string(out), "backend: local")
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
	if roundTripped.Backend.IsLocal || roundTripped.Backend.AWS == nil || *roundTripped.Backend.AWS != *original.Backend.AWS {
		t.Errorf("round-trip mismatch: got %+v, want %+v", roundTripped.Backend, original.Backend)
	}
}
