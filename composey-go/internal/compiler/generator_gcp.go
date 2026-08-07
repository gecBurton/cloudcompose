package compiler

import (
	"github.com/gecburton/composey/internal/models"
)

// gcpResourceOrder mirrors _build_resources' if-block order in
// generator_gcp.py exactly. Like Azure's generator, GCP's json.dumps has
// no sort_keys=True, so this order is load-bearing for anyone who wants
// byte-identical output against a live Python run -- no golden file
// exists to check it against here, but the same PyDumpsIndent-based
// approach used for Azure is used again regardless, since it is known to
// be the only correct one for a non-sort_keys json.dumps call.
var gcpResourceOrder = []string{
	"google_cloud_run_service",
	"google_cloud_run_service_iam_member",
	"google_sql_database_instance",
	"google_sql_database",
	"google_redis_instance",
	"google_storage_bucket",
	"google_storage_bucket_iam_member",
	"google_vpc_access_connector",
	"google_compute_global_address",
	"google_compute_managed_ssl_certificate",
	"google_compute_region_network_endpoint_group",
	"google_compute_backend_service",
	"google_compute_url_map",
	"google_compute_target_https_proxy",
	"google_compute_forwarding_rule",
	"google_secret_manager_secret",
	"google_secret_manager_secret_version",
	"docker_image",
	"docker_registry_image",
	"random_password",
}

func gcpResourceBlocks(resources *models.GcpResources) PyOrdered {
	nonEmpty := map[string]any{
		"google_cloud_run_service":                     resources.CloudRunService,
		"google_cloud_run_service_iam_member":          resources.CloudRunServiceIamMember,
		"google_sql_database_instance":                 resources.SqlDatabaseInstance,
		"google_sql_database":                          resources.SqlDatabase,
		"google_redis_instance":                        resources.RedisInstance,
		"google_storage_bucket":                        resources.StorageBucket,
		"google_storage_bucket_iam_member":             resources.StorageBucketIamMember,
		"google_vpc_access_connector":                  resources.VpcAccessConnector,
		"google_compute_global_address":                resources.ComputeGlobalAddress,
		"google_compute_managed_ssl_certificate":       resources.ComputeManagedSslCertificate,
		"google_compute_region_network_endpoint_group": resources.ComputeRegionNetworkEndpointGroup,
		"google_compute_backend_service":               resources.ComputeBackendService,
		"google_compute_url_map":                       resources.ComputeUrlMap,
		"google_compute_target_https_proxy":            resources.ComputeTargetHttpsProxy,
		"google_compute_forwarding_rule":               resources.ComputeForwardingRule,
		"google_secret_manager_secret":                 resources.SecretManagerSecret,
		"google_secret_manager_secret_version":         resources.SecretManagerSecretVersion,
		"docker_image":                                 resources.DockerImage,
		"docker_registry_image":                        resources.DockerRegistryImage,
		"random_password":                              resources.RandomPassword,
	}

	result := PyOrdered{}
	for _, resourceType := range gcpResourceOrder {
		value := nonEmpty[resourceType]
		if !isNonEmptyMapGcp(value) {
			continue
		}
		result = append(result, p(resourceType, structToPyOrdered(value)))
	}
	return result
}

func isNonEmptyMapGcp(v any) bool {
	switch m := v.(type) {
	case map[string]models.CloudRunService:
		return len(m) > 0
	case map[string]models.CloudRunServiceIamMember:
		return len(m) > 0
	case map[string]models.CloudSqlInstance:
		return len(m) > 0
	case map[string]models.CloudSqlDatabase:
		return len(m) > 0
	case map[string]models.RedisInstance:
		return len(m) > 0
	case map[string]models.StorageBucket:
		return len(m) > 0
	case map[string]models.StorageBucketIamMember:
		return len(m) > 0
	case map[string]models.VpcConnector:
		return len(m) > 0
	case map[string]models.GlobalAddress:
		return len(m) > 0
	case map[string]models.ComputeManagedSslCertificate:
		return len(m) > 0
	case map[string]models.ComputeRegionNetworkEndpointGroup:
		return len(m) > 0
	case map[string]models.ComputeBackendService:
		return len(m) > 0
	case map[string]models.ComputeUrlMap:
		return len(m) > 0
	case map[string]models.ComputeTargetHttpsProxy:
		return len(m) > 0
	case map[string]models.ComputeForwardingRule:
		return len(m) > 0
	case map[string]models.SecretManagerSecret:
		return len(m) > 0
	case map[string]models.SecretManagerSecretVersion:
		return len(m) > 0
	case map[string]models.DockerImage:
		return len(m) > 0
	case map[string]models.DockerRegistryImage:
		return len(m) > 0
	case map[string]any:
		return len(m) > 0
	default:
		return false
	}
}

// GenerateGcp renders a Terraform JSON manifest for the given GCP
// resources and environment, mirroring generator_gcp.py's generate().
func GenerateGcp(resources *models.GcpResources, env *models.GcpEnvironment) (string, error) {
	requiredProviders := PyOrdered{
		p("google", PyOrdered{p("source", "hashicorp/google"), p("version", "~> 5.0")}),
		p("docker", PyOrdered{p("source", "kreuzwerker/docker"), p("version", "~> 3.0")}),
		p("random", PyOrdered{p("source", "hashicorp/random"), p("version", "~> 3.6")}),
	}

	googleProvider := PyOrdered{
		p("project", env.ProjectID),
		p("region", env.Region),
	}
	if env.GcpEndpoint != nil && *env.GcpEndpoint != "" {
		googleProvider = append(googleProvider, p("credentials", *env.GcpEndpoint))
	}

	provider := PyOrdered{
		p("google", googleProvider),
		p("docker", PyOrdered{}),
		p("random", PyOrdered{}),
	}

	terraformDoc := PyOrdered{
		p("terraform", PyOrdered{p("required_providers", requiredProviders)}),
		p("provider", provider),
		p("resource", gcpResourceBlocks(resources)),
	}

	return PyDumpsIndent(terraformDoc, 2), nil
}
