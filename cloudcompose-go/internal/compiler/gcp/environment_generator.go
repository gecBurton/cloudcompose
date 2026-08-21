package gcp

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// GenerateGcpEnvironment generates Terraform JSON for a shared GCP
// environment: a VPC network, subnet, VPC connector for Cloud Run, and
// a service networking connection for Cloud SQL.
//
// backend, if non-nil, is emitted both as this environment's
// `terraform { backend "gcs" {...} }` block and as an `output "backend"`
// block so it can be reused when compiling apps against this environment.
func GenerateGcpEnvironment(
	name, region, vpcCIDR, projectID, domain string,
	tags map[string]string,
	retainDataOnDestroy bool,
	backend *models.BackendConfig,
) (string, error) {
	tfn := shared.TfName(name)

	requiredProviders := map[string]any{
		"google": map[string]any{"source": "hashicorp/google", "version": "~> 5.0"},
	}
	terraform := map[string]any{"required_version": ">= 1.5", "required_providers": requiredProviders}
	provider := map[string]any{"google": map[string]any{"region": region}}

	// backendConfig is also emitted into the `output "backend"` block
	// below, so LoadGcpEnvironment can hand it back to apps compiled
	// against this environment.
	//
	// Terraform's gcs backend uses "prefix", not "key", for the
	// per-object path within the bucket, unlike s3/azurerm.
	var backendConfig map[string]any
	if backend != nil && backend.Gcp != nil {
		terraform["backend"] = map[string]any{
			"gcs": map[string]any{
				"bucket": backend.Gcp.Bucket,
				"prefix": shared.BackendKeyForEnvironment(name),
			},
		}
		backendConfig = map[string]any{"bucket": backend.Gcp.Bucket}
	}

	resource := map[string]any{}

	resource["google_compute_network"] = map[string]any{
		tfn: map[string]any{
			"name":                    name + "-vpc",
			"auto_create_subnetworks": false,
		},
	}

	resource["google_compute_subnetwork"] = map[string]any{
		tfn: map[string]any{
			"name":                     name + "-subnet",
			"region":                   region,
			"network":                  fmt.Sprintf("${google_compute_network.%s.id}", tfn),
			"ip_cidr_range":            vpcCIDR,
			"private_ip_google_access": true,
		},
	}

	connectorCIDR, err := shared.Cidrsubnet(vpcCIDR, 4, 1)
	if err != nil {
		return "", err
	}
	resource["google_vpc_access_connector"] = map[string]any{
		tfn: map[string]any{
			"name":           name + "-connector",
			"region":         region,
			"network":        fmt.Sprintf("${google_compute_network.%s.id}", tfn),
			"ip_cidr_range":  connectorCIDR,
			"min_throughput": 200,
			"max_throughput": 400,
		},
	}

	resource["google_compute_global_address"] = map[string]any{
		tfn + "_service_networking": map[string]any{
			"name":          name + "-service-networking",
			"purpose":       "VPC_PEERING",
			"address_type":  "INTERNAL",
			"prefix_length": 16,
			"network":       fmt.Sprintf("${google_compute_network.%s.id}", tfn),
		},
	}

	resource["google_service_networking_connection"] = map[string]any{
		tfn: map[string]any{
			"network":                 fmt.Sprintf("${google_compute_network.%s.id}", tfn),
			"service":                 "servicenetworking.googleapis.com",
			"reserved_peering_ranges": []string{fmt.Sprintf("${google_compute_global_address.%s_service_networking.name}", tfn)},
		},
	}

	environmentConfig := map[string]any{
		"target":                 "gcp",
		"name":                   name,
		"region":                 region,
		"project_id":             projectID,
		"vpc_id":                 fmt.Sprintf("${google_compute_network.%s.id}", tfn),
		"subnet_id":              fmt.Sprintf("${google_compute_subnetwork.%s.id}", tfn),
		"vpc_connector_name":     fmt.Sprintf("${google_vpc_access_connector.%s.name}", tfn),
		"retain_data_on_destroy": retainDataOnDestroy,
	}
	if len(tags) > 0 {
		environmentConfig["labels"] = tags
	}
	if domain != "" {
		environmentConfig["domain"] = domain
	}

	outputs := map[string]any{
		"environment": map[string]any{
			"description": "Values matching cloudcompose's Environment model.",
			"value":       environmentConfig,
		},
	}
	if backendConfig != nil {
		outputs["backend"] = map[string]any{
			"description": "This environment's own backend config (provider name plus bucket), so every app compiled against this environment can derive its own backend under the same bucket. See docs/multi-user-state.md.",
			"value":       map[string]any{"provider": "gcp", "gcp": backendConfig},
		}
	}

	manifest := map[string]any{
		"terraform": terraform,
		"provider":  provider,
		"resource":  resource,
		"output":    outputs,
	}

	return shared.MarshalIndentedJSON(manifest)
}
