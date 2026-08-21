package shared

import "fmt"

// CommonEnvelope holds the environment.yaml-authored fields common to
// every cloud's Environment struct (Name, Region, LogRetentionDays,
// RetainDataOnDestroy, Tags, and -- AWS/Azure only --
// HighAvailabilityEnabled/BackupRetentionDays), decoded from a Terraform
// `environment` output's raw map[string]any/[]any/string/float64/bool shape.
//
// Every field except Name/Tags is a pointer so an absent key in raw can
// be told apart from one present with the zero value: each loader's own
// constructor sets a default for fields like Region, and an absent field
// must leave that default alone rather than zeroing it out.
type CommonEnvelope struct {
	Name                    string
	Region                  *string
	LogRetentionDays        *int
	RetainDataOnDestroy     *bool
	HighAvailabilityEnabled *bool
	BackupRetentionDays     *int
	Tags                    map[string]string
}

// DecodeCommonEnvelope decodes CommonEnvelope's fields out of raw (the
// map TerraformOutputs returns for the `environment` output), leaving
// each pointer field nil if its key is absent or not the expected JSON
// type.
func DecodeCommonEnvelope(raw map[string]any) CommonEnvelope {
	var e CommonEnvelope
	e.Name, _ = raw["name"].(string)
	if region, ok := raw["region"].(string); ok && region != "" {
		e.Region = &region
	}
	if v, ok := raw["log_retention_days"].(float64); ok {
		n := int(v)
		e.LogRetentionDays = &n
	}
	if v, ok := raw["retain_data_on_destroy"].(bool); ok {
		e.RetainDataOnDestroy = &v
	}
	if v, ok := raw["high_availability_enabled"].(bool); ok {
		e.HighAvailabilityEnabled = &v
	}
	if v, ok := raw["backup_retention_days"].(float64); ok {
		n := int(v)
		e.BackupRetentionDays = &n
	}
	e.Tags = ToStringMap(raw["tags"])
	return e
}

// RequireTarget validates raw's declared "target" field against want,
// defaulting to want when target is absent.
func RequireTarget(raw map[string]any, dir, want string) error {
	target, _ := raw["target"].(string)
	if target == "" {
		target = want
	}
	if target != want {
		return fmt.Errorf("%s declares target %q; this loader only supports %q", dir, target, want)
	}
	return nil
}
