package aws

import (
	"fmt"
	"os"

	"github.com/gecburton/composey/internal/models"
	yaml "go.yaml.in/yaml/v4"
)

// awsEnvironmentYAML mirrors the on-disk shape of an AWS environment file,
// matching the field names AwsEnvironment declares in environment.py
// (snake_case, since these are hand-written YAML files, not Go/JSON
// values). Kept separate from models.AwsEnvironment rather than adding
// yaml tags directly to it: the JSON tags there are load-bearing for this
// package's own Terraform-JSON-shaped output, and reusing them for YAML
// input risks the two drifting for the wrong reason if either format's
// needs change independently later.
type awsEnvironmentYAML struct {
	Target              string            `yaml:"target"`
	Name                string            `yaml:"name"`
	Region              string            `yaml:"region"`
	LogRetentionDays    *int              `yaml:"log_retention_days"`
	RetainDataOnDestroy *bool             `yaml:"retain_data_on_destroy"`
	Tags                map[string]string `yaml:"tags"`
	VpcID               string            `yaml:"vpc_id"`
	PublicSubnets       []string          `yaml:"public_subnets"`
	PrivateSubnets      []string          `yaml:"private_subnets"`
	EcsClusterArn       string            `yaml:"ecs_cluster_arn"`
	AlbArn              *string           `yaml:"alb_arn"`
	AlbListenerArn      *string           `yaml:"alb_listener_arn"`
	AlbSecurityGroupID  *string           `yaml:"alb_security_group_id"`
	AwsEndpoint         *string           `yaml:"aws_endpoint"`
}

// LoadAwsEnvironment loads and validates an AWS environment YAML file,
// mirroring environment.py's load_environment for the "aws" target
// specifically (Azure/GCP environment loading belongs to Phase 4, once
// their inference is ported).
func LoadAwsEnvironment(path string) (*models.AwsEnvironment, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read environment file %s: %w", path, err)
	}

	var parsed awsEnvironmentYAML
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse environment file %s: %w", path, err)
	}

	if parsed.Target == "" {
		parsed.Target = "aws"
	}
	if parsed.Target != "aws" {
		return nil, fmt.Errorf(
			"%s declares target %q; this build only supports \"aws\" (Azure/GCP inference is Phase 4, still Python-only)",
			path, parsed.Target,
		)
	}

	env := models.NewAwsEnvironment()
	env.Name = parsed.Name
	if parsed.Region != "" {
		env.Region = parsed.Region
	}
	if parsed.LogRetentionDays != nil {
		env.LogRetentionDays = *parsed.LogRetentionDays
	}
	if parsed.RetainDataOnDestroy != nil {
		env.RetainDataOnDestroy = *parsed.RetainDataOnDestroy
	}
	env.Tags = parsed.Tags
	env.VpcID = parsed.VpcID
	env.PublicSubnets = parsed.PublicSubnets
	env.PrivateSubnets = parsed.PrivateSubnets
	env.EcsClusterArn = parsed.EcsClusterArn
	env.AlbArn = parsed.AlbArn
	env.AlbListenerArn = parsed.AlbListenerArn
	env.AlbSecurityGroupID = parsed.AlbSecurityGroupID
	env.AwsEndpoint = parsed.AwsEndpoint

	if err := env.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &env, nil
}
