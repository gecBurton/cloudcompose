package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// errMissingOutput is TerraformOutputs' own sentinel for "this
// directory's state has no output named outputName" -- returned
// wrapped (via %w) so OptionalTerraformOutputs can distinguish it from
// every other failure (terraform not on PATH, no state applied yet, a
// genuinely different output being the wrong shape) via errors.As,
// rather than by matching against the error message's own text, which
// would silently stop working the moment either message's wording
// changed.
type errMissingOutput struct {
	dir, outputName string
}

func (e *errMissingOutput) Error() string {
	return fmt.Sprintf(
		"%s has no %q output; has this environment's `terraform apply` run yet?",
		e.dir, e.outputName,
	)
}

// TerraformOutputs runs `terraform output -json` in dir and returns the
// named output's resolved value as a map, or an error if the output
// doesn't exist or isn't itself an object (cloudcompose's own environment
// generators always declare a single `environment` output whose value is
// an object -- see internal/compiler/{aws,azure,gcp}/environment_generator.go).
//
// This is how cloudcompose main resolves an environment's facts (VPC ID, ALB
// ARN, cluster ARN) today: by asking Terraform directly for its own
// current state, rather than reading a file a `local_file` resource
// wrote as a side effect of a specific `apply` run. See
// docs/authored-environment-config.md for why: the file was itself
// redundant with what `terraform output` already tracks, and reading
// live state means there's nothing to go stale -- no drift-detection
// mechanism is needed because there's no cached copy to drift from.
//
// Requires the `terraform` CLI on PATH and a real, already-applied
// Terraform state in dir -- cloudcompose main was already unusable before
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
		return nil, &errMissingOutput{dir: dir, outputName: outputName}
	}

	value, ok := entry.Value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s output %q is not an object (got %T)", dir, outputName, entry.Value)
	}

	return value, nil
}

// OptionalTerraformOutputs behaves exactly like TerraformOutputs, except
// a missing outputName is not an error -- it returns (nil, nil) instead.
// Used for the `backend` output specifically (see
// internal/compiler/{aws,azure,gcp}/environment_generator.go): an
// environment with no backend: configured (today's default; see
// docs/multi-user-state.md) legitimately has no `backend` output at
// all, unlike `environment`, which every environment this codebase
// generates always declares.
func OptionalTerraformOutputs(dir, outputName string) (map[string]any, error) {
	value, err := TerraformOutputs(dir, outputName)
	var missing *errMissingOutput
	if errors.As(err, &missing) {
		return nil, nil
	}
	return value, err
}
