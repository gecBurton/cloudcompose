package compiler

import (
	"fmt"
	"os"

	yaml "go.yaml.in/yaml/v4"
)

// LoadEnvironment loads an environment file, dispatching to the
// cloud-specific loader based on its declared target, mirroring
// environment.py's load_environment.
//
// Returns one of *models.AwsEnvironment, *models.AzureEnvironment, or
// *models.GcpEnvironment as `any`, since Go has no common concrete
// environment type the way Python's BaseEnvironment is (each cloud's
// loader already returns its own pointer type, and this package's own
// InferAWS/InferAzure/InferGcp each take that specific type directly --
// there is no shared inference entrypoint to return a common interface
// for). Callers switch on the concrete type, the same way cli.go and
// compiler/__init__.py's compile_to_terraform do in Python via
// isinstance().
func LoadEnvironment(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read environment file %s: %w", path, err)
	}

	var probe struct {
		Target string `yaml:"target"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("parse environment file %s: %w", path, err)
	}

	target := probe.Target
	if target == "" {
		target = "aws" // DEFAULT_TARGET in environment.py
	}

	switch target {
	case "aws":
		return LoadAwsEnvironment(path)
	case "azure":
		return LoadAzureEnvironment(path)
	case "gcp":
		return LoadGcpEnvironment(path)
	default:
		return nil, fmt.Errorf(
			"%s declares unsupported target %q. Supported targets: aws, azure, gcp.",
			path, target,
		)
	}
}
