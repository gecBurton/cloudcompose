package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// errMissingOutput is TerraformOutputs' sentinel for "this directory's
// state has no output named outputName", returned wrapped (via %w) so
// OptionalTerraformOutputs can distinguish it from other failures via
// errors.As.
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
// doesn't exist or isn't itself an object. Requires the `terraform` CLI
// on PATH and an already-applied Terraform state in dir.
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

// OptionalTerraformOutputs behaves like TerraformOutputs, except a
// missing outputName is not an error -- it returns (nil, nil) instead.
func OptionalTerraformOutputs(dir, outputName string) (map[string]any, error) {
	value, err := TerraformOutputs(dir, outputName)
	var missing *errMissingOutput
	if errors.As(err, &missing) {
		return nil, nil
	}
	return value, err
}
