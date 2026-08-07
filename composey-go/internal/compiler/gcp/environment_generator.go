package gcp

import (
	"fmt"

	"github.com/gecburton/composey/internal/compiler/shared"
)

// GenerateGcpEnvironment generates Terraform JSON for a shared GCP
// environment, mirroring environment_generator.py. Creates a VPC
// Network, subnet, VPC connector for Cloud Run, and a service networking
// connection for Cloud SQL.
func GenerateGcpEnvironment(
	name, region, vpcCIDR string,
	tags map[string]string,
	retainDataOnDestroy bool,
) (string, error) {
	tfn := shared.TfName(name)

	requiredProviders := map[string]any{
		"google": map[string]any{"source": "hashicorp/google", "version": "~> 5.0"},
		"local":  map[string]any{"source": "hashicorp/local", "version": "~> 2.4"},
	}
	terraform := map[string]any{"required_version": ">= 1.5", "required_providers": requiredProviders}
	provider := map[string]any{"google": map[string]any{"region": region}}

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
		"vpc_id":                 fmt.Sprintf("${google_compute_network.%s.id}", tfn),
		"subnet_id":              fmt.Sprintf("${google_compute_subnetwork.%s.id}", tfn),
		"vpc_connector_name":     fmt.Sprintf("${google_vpc_access_connector.%s.name}", tfn),
		"retain_data_on_destroy": retainDataOnDestroy,
	}
	if len(tags) > 0 {
		environmentConfig["labels"] = tags
	}

	environmentConfigJSON, err := shared.MarshalJSONStringPlain(environmentConfig)
	if err != nil {
		return "", err
	}

	resource["local_file"] = map[string]any{
		tfn + "_environment": map[string]any{
			"filename":        "${path.module}/environment.yml",
			"content":         environmentConfigJSON,
			"file_permission": "0644",
		},
	}

	outputs := map[string]any{
		"environment": map[string]any{
			"description": "Values matching composey's Environment model.",
			"value":       environmentConfig,
		},
	}

	manifest := map[string]any{
		"terraform": terraform,
		"provider":  provider,
		"resource":  resource,
		"output":    outputs,
	}

	return shared.MarshalIndentedJSON(manifest)
}
