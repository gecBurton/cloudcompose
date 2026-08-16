package aws

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// LoadAwsEnvironment resolves an AWS environment's facts by running
// `terraform output -json` in dir and decoding its `environment` output
// into models.AwsEnvironment. Requires dir to be a directory
// `cloudcompose init` generated and `terraform apply` has already run in --
// see internal/compiler/shared/terraform_outputs.go for why this reads
// Terraform's own live state rather than a generated file.
func LoadAwsEnvironment(dir string) (*models.AwsEnvironment, error) {
	raw, err := shared.TerraformOutputs(dir, "environment")
	if err != nil {
		return nil, err
	}
	if err := shared.RequireTarget(raw, dir, "aws"); err != nil {
		return nil, err
	}

	env := models.NewAwsEnvironment()
	common := shared.DecodeCommonEnvelope(raw)
	env.Name = common.Name
	if common.Region != nil {
		env.Region = *common.Region
	}
	if common.LogRetentionDays != nil {
		env.LogRetentionDays = *common.LogRetentionDays
	}
	if common.RetainDataOnDestroy != nil {
		env.RetainDataOnDestroy = *common.RetainDataOnDestroy
	}
	if common.HighAvailabilityEnabled != nil {
		env.HighAvailabilityEnabled = *common.HighAvailabilityEnabled
	}
	if common.BackupRetentionDays != nil {
		env.BackupRetentionDays = *common.BackupRetentionDays
	}
	env.Tags = common.Tags

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
