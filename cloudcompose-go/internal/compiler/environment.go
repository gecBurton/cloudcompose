package compiler

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/compiler/gcp"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
)

// LoadEnvironment resolves an environment's facts by running `terraform
// output -json` in dir and dispatching to the cloud-specific loader
// based on its declared target. Returns one of *models.AwsEnvironment,
// *models.AzureEnvironment, or *models.GcpEnvironment as `any`; callers
// switch on the concrete type.
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
