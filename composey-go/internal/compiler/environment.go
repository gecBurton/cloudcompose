package compiler

import (
	"fmt"

	"github.com/gecburton/composey/internal/compiler/aws"
	"github.com/gecburton/composey/internal/compiler/azure"
	"github.com/gecburton/composey/internal/compiler/gcp"
	"github.com/gecburton/composey/internal/compiler/shared"
)

// LoadEnvironment resolves an environment's facts by running `terraform
// output -json` in dir (an environment directory `composey init`
// generated, with `terraform apply` already run in it) and dispatching
// to the cloud-specific loader based on its declared target. See
// internal/compiler/shared/terraform_outputs.go and
// aws.LoadAwsEnvironment's own doc comments for why this reads
// Terraform's own live state rather than a generated file.
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
func LoadEnvironment(dir string) (any, error) {
	raw, err := shared.TerraformOutputs(dir, "environment")
	if err != nil {
		return nil, err
	}

	target, _ := raw["target"].(string)
	if target == "" {
		target = "aws" // DEFAULT_TARGET in environment.py
	}

	switch target {
	case "aws":
		return aws.LoadAwsEnvironment(dir)
	case "azure":
		return azure.LoadAzureEnvironment(dir)
	case "gcp":
		return gcp.LoadGcpEnvironment(dir)
	default:
		return nil, fmt.Errorf(
			"%s declares unsupported target %q. Supported targets: aws, azure, gcp.",
			dir, target,
		)
	}
}
