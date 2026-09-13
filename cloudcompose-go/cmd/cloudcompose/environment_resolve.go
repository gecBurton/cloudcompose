package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gecburton/cloudcompose/internal/compiler"
	"github.com/gecburton/cloudcompose/internal/compiler/initconfig"
	"github.com/gecburton/cloudcompose/internal/models"
)

// resolveEnvironmentByDefinition resolves an environment directly from
// its authored environment.yaml, without needing the caller to already
// know where a previous `env init`/`env up` wrote its output
// directory -- but never creates or modifies that directory itself.
// Environment changes are deliberate acts (`env init`/`env up`), never
// a side effect of deploying an app: this fails clearly if the
// environment hasn't been applied yet, rather than applying it on the
// caller's behalf. See docs/deployment-identity-design.md.
func resolveEnvironmentByDefinition(environmentYamlPath string) (any, error) {
	_, dir, err := environmentDirFromDefinition(environmentYamlPath)
	if err != nil {
		return nil, err
	}
	return compiler.LoadEnvironment(dir)
}

// environmentDirFromDefinition derives <dir of environmentYamlPath>/
// env-<name> -- the same path `env init`/`env up` themselves compute --
// without creating or modifying it, and confirms it already exists
// (see resolveEnvironmentByDefinition's own doc comment for why).
// Returns the loaded config alongside the directory, since some
// callers (e.g. appDir) need the environment's own name without
// re-reading the file.
func environmentDirFromDefinition(environmentYamlPath string) (*models.InitConfig, string, error) {
	fileConfig, err := initconfig.Load(environmentYamlPath)
	if err != nil {
		return nil, "", err
	}
	if fileConfig == nil {
		return nil, "", fmt.Errorf(
			"%s not found -- pass the authored environment.yaml that "+
				"produced the environment you mean, not an already-applied "+
				"output directory",
			environmentYamlPath,
		)
	}

	absConfigFile, err := filepath.Abs(environmentYamlPath)
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(filepath.Dir(absConfigFile), "env-"+fileConfig.Name)
	if _, statErr := os.Stat(dir); statErr != nil {
		return nil, "", fmt.Errorf(
			"%s has not been applied yet -- run `cloud-compose env init -e %s` "+
				"then `terraform apply` in %s (or `cloud-compose env up -e %s`) first",
			environmentYamlPath, environmentYamlPath, dir, environmentYamlPath,
		)
	}

	return fileConfig, dir, nil
}
