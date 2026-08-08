package aws

import (
	"fmt"

	"github.com/gecburton/composey/internal/compiler/shared"
	"github.com/gecburton/composey/internal/models"
)

// LoadAwsEnvironment resolves an AWS environment's facts by running
// `terraform output -json` in dir and decoding its `environment` output
// into models.AwsEnvironment. Requires dir to be a directory
// `composey init` generated and `terraform apply` has already run in --
// see internal/compiler/shared/terraform_outputs.go for why this reads
// Terraform's own live state rather than a generated file.
func LoadAwsEnvironment(dir string) (*models.AwsEnvironment, error) {
	raw, err := shared.TerraformOutputs(dir, "environment")
	if err != nil {
		return nil, err
	}

	target, _ := raw["target"].(string)
	if target == "" {
		target = "aws"
	}
	if target != "aws" {
		return nil, fmt.Errorf(
			"%s declares target %q; this loader only supports \"aws\"",
			dir, target,
		)
	}

	env := models.NewAwsEnvironment()
	env.Name, _ = raw["name"].(string)
	if region, ok := raw["region"].(string); ok && region != "" {
		env.Region = region
	}
	if logRetentionDays, ok := raw["log_retention_days"].(float64); ok {
		env.LogRetentionDays = int(logRetentionDays)
	}
	if retainData, ok := raw["retain_data_on_destroy"].(bool); ok {
		env.RetainDataOnDestroy = retainData
	}
	env.Tags = shared.ToStringMap(raw["tags"])
	env.VpcID, _ = raw["vpc_id"].(string)
	env.PublicSubnets = shared.ToStringSlice(raw["public_subnets"])
	env.PrivateSubnets = shared.ToStringSlice(raw["private_subnets"])
	env.EcsClusterArn, _ = raw["ecs_cluster_arn"].(string)
	env.AlbArn = shared.ToStringPtr(raw["alb_arn"])
	env.AlbListenerArn = shared.ToStringPtr(raw["alb_listener_arn"])
	env.AlbSecurityGroupID = shared.ToStringPtr(raw["alb_security_group_id"])
	env.AwsEndpoint = shared.ToStringPtr(raw["aws_endpoint"])

	if err := env.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}

	return &env, nil
}
