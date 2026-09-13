package main

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler"
	"github.com/gecburton/cloudcompose/internal/compiler/initconfig"
)

// resolveEnvironmentByDefinition takes an authored environment.yaml
// path and returns the same environment facts LoadEnvironment(dir)
// would, without requiring the caller to have already run `env init`/
// `env up` and to know where that wrote its output directory.
//
// Unlike --env <dir> (an already-applied environment directory, found
// by the operator with no recorded link back to whatever
// environment.yaml produced it), this is the portable, durable handle
// described in docs/deployment-identity-design.md: given the same
// environment.yaml and cloud credentials, this always reconnects to
// the same deployed environment, regardless of whether -- or where --
// its generated Terraform directory currently exists on disk.
//
// That durability requires a remote backend: environmentYamlPath's
// `backend:` block is what makes the environment's state key
// (BackendKeyForEnvironment(name), derived from name: alone) a stable
// locator independent of any local directory. Without one, state is
// local to whichever directory happens to hold it, and there is no way
// to "reconnect" to it from the environment.yaml alone -- so this
// entry point requires backend: to be set, rather than silently
// falling back to local state the way `env init`/`env up` do (where
// local state remains a legitimate, explicitly-warned-about,
// single-developer choice).
func resolveEnvironmentByDefinition(environmentYamlPath string) (any, error) {
	fileConfig, err := initconfig.Load(environmentYamlPath)
	if err != nil {
		return nil, err
	}
	if fileConfig == nil {
		return nil, fmt.Errorf(
			"%s not found -- pass the authored environment.yaml that "+
				"produced the environment you mean, not an already-applied "+
				"output directory (see docs/deployment-identity-design.md)",
			environmentYamlPath,
		)
	}
	if fileConfig.Backend == nil {
		return nil, fmt.Errorf(
			"%s has no `backend:` configured. This entry point resolves an "+
				"environment from environment.yaml alone, which requires its "+
				"state to be durably locatable by name -- not merely wherever "+
				"a local terraform.tfstate happens to sit. Add a `backend:` "+
				"block (see docs/authored-environment-config.md), or use "+
				"--env <directory> to point at an already-applied environment "+
				"directly.",
			environmentYamlPath,
		)
	}

	// initEnvironment is idempotent: it always (re)writes
	// main.tf.json from fileConfig, so it's safe to call whether or
	// not this environment's output directory already exists, and
	// whether or not it's stale relative to environmentYamlPath.
	dir, err := initEnvironment(environmentYamlPath)
	if err != nil {
		return nil, err
	}

	// terraform init (re-)connects dir to the backend's state key
	// derived from fileConfig.Name, regardless of whether dir is a
	// fresh directory or a stale one from a previous run elsewhere --
	// the backend key, not the directory, is the durable identity
	// here.
	if err := terraformInit(dir); err != nil {
		return nil, err
	}

	return compiler.LoadEnvironment(dir)
}
