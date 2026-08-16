package shared

import "fmt"

// CommonEnvelope holds the environment.yaml-authored fields every
// cloud's own Aws/Azure/GcpEnvironment struct declares identically
// (Name, Region, LogRetentionDays, RetainDataOnDestroy, Tags, and --
// AWS/Azure only -- HighAvailabilityEnabled/BackupRetentionDays), once
// decoded from a Terraform `environment` output's raw
// map[string]any/[]any/string/float64/bool shape.
//
// This -- and DecodeCommonEnvelope below -- replaces what used to be
// the identical field-by-field decode logic hand-copied into
// aws/azure/gcp/environment.go's own LoadAwsEnvironment/
// LoadAzureEnvironment/LoadGcpEnvironment: a future change to the
// common envelope (e.g. a new shared field) previously meant editing 3
// files identically, which is exactly the kind of drift this package's
// own size-table consolidation (see docs/azure-aws-parity-todo.md) had
// to fix once already for a different duplicated table. Each loader
// now decodes once via DecodeCommonEnvelope and copies whichever fields
// it has, e.g.:
//
//	common := shared.DecodeCommonEnvelope(raw)
//	env.Name = common.Name
//	if common.Region != nil { env.Region = *common.Region }
//	if common.LogRetentionDays != nil { env.LogRetentionDays = *common.LogRetentionDays }
//	// ...
//
// Every field except Name/Tags is a pointer so a genuinely absent key
// in raw can be told apart from one present with the zero value: each
// loader's own NewXEnvironment() constructor already sets a sensible
// default for fields like Region, and an absent field must leave that
// default alone rather than zeroing it out -- the same "only overwrite
// what was actually present" rule every loader applied field-by-field
// before this existed.
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
// defaulting to want when target is absent (matching DEFAULT_TARGET in
// the original Python implementation -- see LoadEnvironment's own doc
// comment in internal/compiler/environment.go). Each of
// LoadAwsEnvironment/LoadAzureEnvironment/LoadGcpEnvironment calls this
// immediately after resolving raw, before decoding anything
// cloud-specific: an environment.yaml declaring the wrong target for
// the loader reading it must fail immediately, not partway through
// decoding fields that don't exist in the wrong shape.
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
