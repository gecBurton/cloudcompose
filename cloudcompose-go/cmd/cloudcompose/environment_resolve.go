package main

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler"
	"github.com/gecburton/cloudcompose/internal/compiler/initconfig"
)

// resolveEnvironmentByDefinition resolves an environment directly from
// its authored environment.yaml, without needing the caller to already
// know where a previous `env init`/`env up` wrote its output
// directory. Requires a `backend:` block: state has to be durably
// locatable by name, not just wherever a local terraform.tfstate
// happens to sit. See docs/deployment-identity-design.md.
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
	if fileConfig.Backend == nil {
		return nil, fmt.Errorf(
			"%s has no `backend:` configured -- required to resolve an "+
				"environment from environment.yaml alone (see "+
				"docs/authored-environment-config.md), or use --env "+
				"<directory> instead",
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
