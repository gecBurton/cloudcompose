package gcp

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// LoadGcpEnvironment resolves a GCP environment's facts by running
// `terraform output -json` in dir and decoding its `environment` output
// into models.GcpEnvironment.
func LoadGcpEnvironment(dir string) (*models.GcpEnvironment, error) {
	raw, err := shared.TerraformOutputs(dir, "environment")
	if err != nil {
		return nil, err
	}
	if err := shared.RequireTarget(raw, dir, "gcp"); err != nil {
		return nil, err
	}

	env := models.NewGcpEnvironment()
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
	// GcpEnvironment has no HighAvailabilityEnabled/BackupRetentionDays
	// fields, so those common fields are decoded but not copied here.
	env.Tags = common.Tags

	env.ProjectID, _ = raw["project_id"].(string)
	env.VpcID = shared.ToStringPtr(raw["vpc_id"])
	env.SubnetIDs = shared.ToStringSlice(raw["subnet_ids"])
	env.CloudSqlInstanceID = shared.ToStringPtr(raw["cloud_sql_instance_id"])
	env.ArtifactRegistryRepository = shared.ToStringPtr(raw["artifact_registry_repository"])
	env.LoadBalancerIP = shared.ToStringPtr(raw["load_balancer_ip"])
	env.ServiceAccountEmail = shared.ToStringPtr(raw["service_account_email"])
	env.GcpEndpoint = shared.ToStringPtr(raw["gcp_endpoint"])
	env.Domain = shared.ToStringPtr(raw["domain"])

	backendRaw, err := shared.OptionalTerraformOutputs(dir, "backend")
	if err != nil {
		return nil, err
	}
	env.Backend = shared.DecodeBackendOutput(backendRaw)

	return &env, nil
}
