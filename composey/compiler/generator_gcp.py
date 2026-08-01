"""GCP Terraform JSON generator.

Converts GcpResources to Terraform JSON format for the Google provider.
"""

import json
from typing import Any

from composey.models.environment import GcpEnvironment
from composey.models.gcp import GcpResources


def generate(resources: GcpResources, env: GcpEnvironment) -> str:
    """Generate Terraform JSON for GCP resources.

    Args:
        resources: The GCP resources to generate
        env: The GCP environment configuration

    Returns:
        JSON string containing Terraform configuration
    """
    terraform = {
        "terraform": {
            "required_providers": {
                "google": {
                    "source": "hashicorp/google",
                    "version": "~> 5.0",
                },
                "docker": {
                    "source": "kreuzwerker/docker",
                    "version": "~> 3.0",
                },
                "random": {
                    "source": "hashicorp/random",
                    "version": "~> 3.6",
                },
            },
        },
        "provider": {
            "google": {
                "project": env.project_id,
                "region": env.region,
            },
            "docker": {},
            "random": {},
        },
        "resource": _build_resources(resources),
    }

    # Add custom endpoint if specified (for testing)
    if env.gcp_endpoint:
        terraform["provider"]["google"]["credentials"] = env.gcp_endpoint

    return json.dumps(terraform, indent=2)


def _build_resources(resources: GcpResources) -> dict[str, Any]:
    """Build the resource section of Terraform JSON."""
    result: dict[str, Any] = {}

    # Cloud Run services
    if resources.google_cloud_run_service:
        result["google_cloud_run_service"] = {
            k: _clean_model(v) for k, v in resources.google_cloud_run_service.items()
        }

    if resources.google_cloud_run_service_iam_member:
        result["google_cloud_run_service_iam_member"] = {
            k: _clean_model(v)
            for k, v in resources.google_cloud_run_service_iam_member.items()
        }

    # Cloud SQL
    if resources.google_sql_database_instance:
        result["google_sql_database_instance"] = {
            k: _clean_model(v)
            for k, v in resources.google_sql_database_instance.items()
        }

    if resources.google_sql_database:
        result["google_sql_database"] = {
            k: _clean_model(v) for k, v in resources.google_sql_database.items()
        }

    # Memorystore (Redis)
    if resources.google_redis_instance:
        result["google_redis_instance"] = {
            k: _clean_model(v) for k, v in resources.google_redis_instance.items()
        }

    # Cloud Storage
    if resources.google_storage_bucket:
        result["google_storage_bucket"] = {
            k: _clean_model(v) for k, v in resources.google_storage_bucket.items()
        }

    if resources.google_storage_bucket_iam_member:
        result["google_storage_bucket_iam_member"] = {
            k: _clean_model(v)
            for k, v in resources.google_storage_bucket_iam_member.items()
        }

    # VPC Connector
    if resources.google_vpc_access_connector:
        result["google_vpc_access_connector"] = {
            k: _clean_model(v) for k, v in resources.google_vpc_access_connector.items()
        }

    # Load balancing
    if resources.google_compute_global_address:
        result["google_compute_global_address"] = {
            k: _clean_model(v)
            for k, v in resources.google_compute_global_address.items()
        }

    if resources.google_compute_managed_ssl_certificate:
        result["google_compute_managed_ssl_certificate"] = {
            k: _clean_model(v)
            for k, v in resources.google_compute_managed_ssl_certificate.items()
        }

    if resources.google_compute_region_network_endpoint_group:
        result["google_compute_region_network_endpoint_group"] = {
            k: _clean_model(v)
            for k, v in resources.google_compute_region_network_endpoint_group.items()
        }

    if resources.google_compute_backend_service:
        result["google_compute_backend_service"] = {
            k: _clean_model(v)
            for k, v in resources.google_compute_backend_service.items()
        }

    if resources.google_compute_url_map:
        result["google_compute_url_map"] = {
            k: _clean_model(v) for k, v in resources.google_compute_url_map.items()
        }

    if resources.google_compute_target_https_proxy:
        result["google_compute_target_https_proxy"] = {
            k: _clean_model(v)
            for k, v in resources.google_compute_target_https_proxy.items()
        }

    if resources.google_compute_forwarding_rule:
        result["google_compute_forwarding_rule"] = {
            k: _clean_model(v)
            for k, v in resources.google_compute_forwarding_rule.items()
        }

    # Secret Manager
    if resources.google_secret_manager_secret:
        result["google_secret_manager_secret"] = {
            k: _clean_model(v)
            for k, v in resources.google_secret_manager_secret.items()
        }

    if resources.google_secret_manager_secret_version:
        result["google_secret_manager_secret_version"] = {
            k: _clean_model(v)
            for k, v in resources.google_secret_manager_secret_version.items()
        }

    # Docker resources
    if resources.docker_image:
        result["docker_image"] = resources.docker_image

    if resources.docker_registry_image:
        result["docker_registry_image"] = resources.docker_registry_image

    # Random resources
    if resources.random_password:
        result["random_password"] = {
            k: _clean_model(v) for k, v in resources.random_password.items()
        }

    return result


def _clean_model(obj: Any) -> Any:
    """Clean a Pydantic model for Terraform JSON output."""
    if hasattr(obj, "model_dump"):
        data = obj.model_dump(exclude_none=True)
    elif hasattr(obj, "dict"):
        data = obj.dict(exclude_none=True)
    else:
        return obj

    return data
