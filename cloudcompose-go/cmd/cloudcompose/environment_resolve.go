package main

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler"
	"github.com/gecburton/cloudcompose/internal/compiler/initconfig"
)

// resolveEnvironmentByDefinition resolves an environment directly from
// its authored environment.yaml, without needing the caller to already
// know where a previous `env init`/`env up` wrote its output
// directory. Works for both `local:` and remote backends: `local:`'s
// path is authored and resolved relative to environment.yaml's own
// directory (see models.LocalBackendConfig), so it's just as
// deterministic to regenerate as a remote backend's derived state key
// -- deleting and regenerating the environment's own output directory
// always reconnects to the same state file. See
// docs/deployment-identity-design.md.
func resolveEnvironmentByDefinition(environmentYamlPath string) (any, error) {
	fileConfig, err := initconfig.Load(environmentYamlPath)
	if err != nil {
		return nil, err
	}
	if fileConfig == nil {
		return nil, fmt.Errorf(
			"%s not found -- pass the authored environment.yaml that "+
				"produced the environment you mean, not an already-applied "+
				"output directory",
			environmentYamlPath,
		)
	}

	// initEnvironment (re)writes main.tf.json unconditionally, so this
	// is safe whether or not the output directory already exists.
	dir, err := initEnvironment(environmentYamlPath)
	if err != nil {
		return nil, err
	}
	if err := terraformInit(dir); err != nil {
		return nil, err
	}

	return compiler.LoadEnvironment(dir)
}
