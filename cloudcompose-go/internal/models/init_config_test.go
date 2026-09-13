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

// TestBackendConfig_RoundTripsLocalBlock exercises Local's own
// no-Provider-context round trip, unaffected by AWS/Azure/Gcp's
// custom MarshalYAML handling below.
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

// TestBackendConfig_MarshalsRemoteAwsBlockUnderRemoteKey covers
// BackendConfig's own MarshalYAML: AWS/Azure/Gcp are tagged `yaml:"-"`
// since which one applies depends on InitConfig.Provider (decoded by
// initconfig.Load's decodeBackendRemote, not by BackendConfig itself --
// see BackendConfig's own doc comment), so MarshalYAML is what puts
// whichever one is set back under the single `remote:` key rather than
// a cloud-named one. Decoding the *other* direction (YAML -> struct)
// needs Provider context BackendConfig alone doesn't have, so that's
// covered at the initconfig.Load level instead (see
// TestLoad_BackendAwsConfig).
func TestBackendConfig_MarshalsRemoteAwsBlockUnderRemoteKey(t *testing.T) {
	t.Parallel()
	original := backendWrapper{Backend: BackendConfig{AWS: &AwsBackendConfig{Bucket: "my-bucket", Region: "eu-west-2"}}}
	out, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	got := string(out)
	want := "backend:\n    remote:\n        bucket: my-bucket\n        region: eu-west-2\n"
	if got != want {
		t.Errorf("marshal mismatch: got %q, want %q", got, want)
	}
}

func TestBackendConfig_RoundTripsAwsBlock(t *testing.T) {
	t.Parallel()
	original := backendWrapper{Backend: BackendConfig{AWS: &AwsBackendConfig{Bucket: "my-bucket", Region: "eu-west-2"}}}
	out, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Unmarshalling the marshalled output back into a plain
	// BackendConfig (with no Provider context) can't repopulate AWS --
	// only initconfig.Load's decodeBackendRemote can, since it alone
	// knows which provider's shape `remote:` should be decoded into.
	// This asserts that limitation explicitly rather than leaving it
	// implicit.
	var roundTripped backendWrapper
	if err := yaml.Unmarshal(out, &roundTripped); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if roundTripped.Backend.AWS != nil {
		t.Errorf("expected AWS to stay nil without Provider context, got %+v", roundTripped.Backend.AWS)
	}
}
