package gcp

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// LoadGcpEnvironment resolves a GCP environment's facts by running
// `terraform output -json` in dir and decoding its `environment` output
// into models.GcpEnvironment. See aws.LoadAwsEnvironment's own doc
// comment for why this reads Terraform's own live state rather than a
// generated file, and for the optional `backend` output this also
// decodes into env.Backend.
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
	// fields (see docs/azure-aws-parity-todo.md's "Backup/HA settings
	// not wired for GCP" item -- Cloud SQL has its own equivalent
	// settings, left for a follow-up), so common.HighAvailabilityEnabled/
	// BackupRetentionDays are decoded but deliberately never copied here.
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
