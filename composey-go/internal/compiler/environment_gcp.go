package compiler

import (
	"fmt"
	"os"

	"github.com/gecburton/composey/internal/models"
	yaml "go.yaml.in/yaml/v4"
)

// gcpEnvironmentYAML mirrors the on-disk shape of a GCP environment file,
// matching the field names GcpEnvironment declares in environment.py.
type gcpEnvironmentYAML struct {
	Target              string            `yaml:"target"`
	Name                string            `yaml:"name"`
	Region              string            `yaml:"region"`
	LogRetentionDays    *int              `yaml:"log_retention_days"`
	RetainDataOnDestroy *bool             `yaml:"retain_data_on_destroy"`
	Tags                map[string]string `yaml:"tags"`

	ProjectID string `yaml:"project_id"`

	VpcID     *string  `yaml:"vpc_id"`
	SubnetIDs []string `yaml:"subnet_ids"`

	CloudSqlInstanceID         *string `yaml:"cloud_sql_instance_id"`
	ArtifactRegistryRepository *string `yaml:"artifact_registry_repository"`
	LoadBalancerIP             *string `yaml:"load_balancer_ip"`
	ServiceAccountEmail        *string `yaml:"service_account_email"`
	GcpEndpoint                *string `yaml:"gcp_endpoint"`
}

// LoadGcpEnvironment loads a GCP environment YAML file, mirroring
// environment.py's load_environment for the "gcp" target specifically.
func LoadGcpEnvironment(path string) (*models.GcpEnvironment, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read environment file %s: %w", path, err)
	}

	var parsed gcpEnvironmentYAML
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse environment file %s: %w", path, err)
	}

	if parsed.Target == "" {
		parsed.Target = "gcp"
	}
	if parsed.Target != "gcp" {
		return nil, fmt.Errorf(
			"%s declares target %q; this loader only supports \"gcp\"",
			path, parsed.Target,
		)
	}

	env := models.NewGcpEnvironment()
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
	env.ProjectID = parsed.ProjectID
	env.VpcID = parsed.VpcID
	env.SubnetIDs = parsed.SubnetIDs
	env.CloudSqlInstanceID = parsed.CloudSqlInstanceID
	env.ArtifactRegistryRepository = parsed.ArtifactRegistryRepository
	env.LoadBalancerIP = parsed.LoadBalancerIP
	env.ServiceAccountEmail = parsed.ServiceAccountEmail
	env.GcpEndpoint = parsed.GcpEndpoint

	return &env, nil
}
