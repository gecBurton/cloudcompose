package gcp

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// LoadGcpEnvironment resolves a GCP environment's facts by running
// `terraform output -json` in dir and decoding its `environment` output
// into models.GcpEnvironment. See aws.LoadAwsEnvironment's own doc
// comment for why this reads Terraform's own live state rather than a
// generated file.
func LoadGcpEnvironment(dir string) (*models.GcpEnvironment, error) {
	raw, err := shared.TerraformOutputs(dir, "environment")
	if err != nil {
		return nil, err
	}

	target, _ := raw["target"].(string)
	if target == "" {
		target = "gcp"
	}
	if target != "gcp" {
		return nil, fmt.Errorf(
			"%s declares target %q; this loader only supports \"gcp\"",
			dir, target,
		)
	}

	env := models.NewGcpEnvironment()
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
	env.ProjectID, _ = raw["project_id"].(string)
	env.VpcID = shared.ToStringPtr(raw["vpc_id"])
	env.SubnetIDs = shared.ToStringSlice(raw["subnet_ids"])
	env.CloudSqlInstanceID = shared.ToStringPtr(raw["cloud_sql_instance_id"])
	env.ArtifactRegistryRepository = shared.ToStringPtr(raw["artifact_registry_repository"])
	env.LoadBalancerIP = shared.ToStringPtr(raw["load_balancer_ip"])
	env.ServiceAccountEmail = shared.ToStringPtr(raw["service_account_email"])
	env.GcpEndpoint = shared.ToStringPtr(raw["gcp_endpoint"])
	env.Domain = shared.ToStringPtr(raw["domain"])

	return &env, nil
}
