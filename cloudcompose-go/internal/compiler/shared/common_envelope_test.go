package shared

import (
	"reflect"
	"testing"
)

// TestDecodeCommonEnvelope_DecodesEveryField confirms every
// CommonEnvelope field decodes correctly from a fully-populated raw map
// -- the shape TerraformOutputs returns for a real environment output,
// decoded through encoding/json into map[string]any/[]any/string/
// float64/bool.
func TestDecodeCommonEnvelope_DecodesEveryField(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"name":                      "prod",
		"region":                    "eu-west-2",
		"log_retention_days":        float64(30),
		"retain_data_on_destroy":    true,
		"high_availability_enabled": true,
		"backup_retention_days":     float64(7),
		"tags":                      map[string]any{"team": "platform"},
	}

	got := DecodeCommonEnvelope(raw)

	if got.Name != "prod" {
		t.Errorf("Name = %q, want %q", got.Name, "prod")
	}
	if got.Region == nil || *got.Region != "eu-west-2" {
		t.Errorf("Region = %v, want %q", got.Region, "eu-west-2")
	}
	if got.LogRetentionDays == nil || *got.LogRetentionDays != 30 {
		t.Errorf("LogRetentionDays = %v, want 30", got.LogRetentionDays)
	}
	if got.RetainDataOnDestroy == nil || *got.RetainDataOnDestroy != true {
		t.Errorf("RetainDataOnDestroy = %v, want true", got.RetainDataOnDestroy)
	}
	if got.HighAvailabilityEnabled == nil || *got.HighAvailabilityEnabled != true {
		t.Errorf("HighAvailabilityEnabled = %v, want true", got.HighAvailabilityEnabled)
	}
	if got.BackupRetentionDays == nil || *got.BackupRetentionDays != 7 {
		t.Errorf("BackupRetentionDays = %v, want 7", got.BackupRetentionDays)
	}
	if !reflect.DeepEqual(got.Tags, map[string]string{"team": "platform"}) {
		t.Errorf("Tags = %v, want %v", got.Tags, map[string]string{"team": "platform"})
	}
}

// TestDecodeCommonEnvelope_AbsentFieldsStayNil confirms a raw map
// missing every optional field (only "name" present, the one field
// every real environment output always has) leaves every pointer field
// nil rather than a zero value -- callers rely on this nil-vs-zero
// distinction to leave their own NewXEnvironment() defaults alone for
// fields raw simply doesn't mention (see this function's own doc
// comment on CommonEnvelope for why).
func TestDecodeCommonEnvelope_AbsentFieldsStayNil(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"name": "prod"}

	got := DecodeCommonEnvelope(raw)

	if got.Name != "prod" {
		t.Errorf("Name = %q, want %q", got.Name, "prod")
	}
	if got.Region != nil {
		t.Errorf("Region = %v, want nil", got.Region)
	}
	if got.LogRetentionDays != nil {
		t.Errorf("LogRetentionDays = %v, want nil", got.LogRetentionDays)
	}
	if got.RetainDataOnDestroy != nil {
		t.Errorf("RetainDataOnDestroy = %v, want nil", got.RetainDataOnDestroy)
	}
	if got.HighAvailabilityEnabled != nil {
		t.Errorf("HighAvailabilityEnabled = %v, want nil", got.HighAvailabilityEnabled)
	}
	if got.BackupRetentionDays != nil {
		t.Errorf("BackupRetentionDays = %v, want nil", got.BackupRetentionDays)
	}
	if got.Tags != nil {
		t.Errorf("Tags = %v, want nil", got.Tags)
	}
}

// TestDecodeCommonEnvelope_EmptyRegionStaysNil confirms an
// explicitly-empty-string region (as opposed to an absent one) is
// still treated as "not present" -- matching every loader's own
// pre-existing `if region, ok := raw["region"].(string); ok && region
// != ""` check before this was unified.
func TestDecodeCommonEnvelope_EmptyRegionStaysNil(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"name": "prod", "region": ""}

	got := DecodeCommonEnvelope(raw)

	if got.Region != nil {
		t.Errorf("Region = %v, want nil for an empty string", got.Region)
	}
}

// TestRequireTarget_DefaultsWhenAbsent confirms an environment output
// with no "target" key at all defaults to want -- matching
// DEFAULT_TARGET in the original Python implementation (see
// RequireTarget's own doc comment).
func TestRequireTarget_DefaultsWhenAbsent(t *testing.T) {
	t.Parallel()
	if err := RequireTarget(map[string]any{}, "/tmp/env", "aws"); err != nil {
		t.Errorf("expected no error when target is absent, got: %v", err)
	}
}

// TestRequireTarget_AcceptsMatchingTarget confirms a target that
// matches want passes.
func TestRequireTarget_AcceptsMatchingTarget(t *testing.T) {
	t.Parallel()
	if err := RequireTarget(map[string]any{"target": "azure"}, "/tmp/env", "azure"); err != nil {
		t.Errorf("expected no error for a matching target, got: %v", err)
	}
}

// TestRequireTarget_RejectsMismatchedTarget confirms a target that
// doesn't match want fails with a message naming both the declared and
// expected target.
func TestRequireTarget_RejectsMismatchedTarget(t *testing.T) {
	t.Parallel()
	err := RequireTarget(map[string]any{"target": "gcp"}, "/tmp/env", "aws")
	if err == nil {
		t.Fatal("expected an error for a mismatched target")
	}
	if !contains(err.Error(), "gcp") || !contains(err.Error(), "aws") {
		t.Errorf("expected the error to name both targets, got: %v", err)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
