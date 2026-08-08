package shared

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// TerraformOutputs runs `terraform output -json` in dir and returns the
// named output's resolved value as a map, or an error if the output
// doesn't exist or isn't itself an object (composey's own environment
// generators always declare a single `environment` output whose value is
// an object -- see internal/compiler/{aws,azure,gcp}/environment_generator.go).
//
// This is how composey main resolves an environment's facts (VPC ID, ALB
// ARN, cluster ARN) today: by asking Terraform directly for its own
// current state, rather than reading a file a `local_file` resource
// wrote as a side effect of a specific `apply` run. See
// docs/authored-environment-config.md for why: the file was itself
// redundant with what `terraform output` already tracks, and reading
// live state means there's nothing to go stale -- no drift-detection
// mechanism is needed because there's no cached copy to drift from.
//
// Requires the `terraform` CLI on PATH and a real, already-applied
// Terraform state in dir -- composey main was already unusable before
// `apply` ran even when it read a generated file, since that file is
// itself only written by `apply`; this makes that dependency direct
// instead of mediated through a file.
func TerraformOutputs(dir, outputName string) (map[string]any, error) {
	cmd := exec.Command("terraform", "output", "-json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("terraform output -json in %s: %w\n%s", dir, err, exitErr.Stderr)
		}
		return nil, fmt.Errorf("terraform output -json in %s: %w", dir, err)
	}

	var parsed map[string]struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("parse terraform output -json in %s: %w", dir, err)
	}

	entry, ok := parsed[outputName]
	if !ok {
		return nil, fmt.Errorf(
			"%s has no %q output; has this environment's `terraform apply` run yet?",
			dir, outputName,
		)
	}

	value, ok := entry.Value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s output %q is not an object (got %T)", dir, outputName, entry.Value)
	}

	return value, nil
}
