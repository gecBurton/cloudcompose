"""Azure Terraform JSON generator.

Converts AzureResources to Terraform JSON format for the AzureRM provider.
"""

import json
from typing import Any

from composey.models.azure import AzureResources
from composey.models.environment import AzureEnvironment


def generate(resources: AzureResources, env: AzureEnvironment) -> str:
    """Generate Terraform JSON for Azure resources.

    Args:
        resources: The Azure resources to generate
        env: The Azure environment configuration

    Returns:
        JSON string containing Terraform configuration
    """
    terraform = {
        "terraform": {
            "required_providers": {
                "azurerm": {
                    "source": "hashicorp/azurerm",
                    "version": "~> 4.0",
                },
                "random": {
                    "source": "hashicorp/random",
                    "version": "~> 3.6",
                },
            },
        },
        "provider": {
            "azurerm": {
                "features": {},
            },
            "random": {},
        },
        "data": {
            "azurerm_client_config": {
                "current": {},
            },
            # The Container Apps Environment belongs to the platform stack, not
            # to the application. Look it up rather than declaring it: two
            # stacks that both manage it fight over the same resource, and the
            # app stack loses with "already exists - to be managed via
            # Terraform this resource needs to be imported into the State".
            "azurerm_container_app_environment": {
                "main": {
                    "name": env.container_apps_environment_name,
                    "resource_group_name": env.name,
                },
            },
        },
        "resource": _build_resources(resources),
    }

    # Only wire up the docker provider if something actually builds an image.
    # Auth is against the ACR admin account (azurerm_container_registry has
    # admin_enabled=True whenever it exists) rather than a short-lived token
    # like AWS's aws_ecr_authorization_token data source: ACR's admin
    # credentials are stable resource attributes, so no data source is
    # needed to fetch them.
    if resources.docker_image:
        terraform["terraform"]["required_providers"]["docker"] = {
            "source": "kreuzwerker/docker",
            "version": "~> 3.0",
        }
        terraform["provider"]["docker"] = {
            "registry_auth": [
                {
                    "address": "${azurerm_container_registry.main.login_server}",
                    "username": "${azurerm_container_registry.main.admin_username}",
                    "password": "${azurerm_container_registry.main.admin_password}",
                }
            ]
        }

    # On AWS the public hostname belongs to the environment's shared load
    # balancer, so the environment stack publishes it. A Container App carries
    # its own ingress hostname, so it has to be published here or nothing
    # downstream can reach the deployed application.
    fqdn = _ingress_fqdn(resources)
    if fqdn:
        terraform["output"] = {
            "fqdn": {
                "description": "Public hostname of the ingress-enabled service.",
                "value": fqdn,
            }
        }

    # Add custom endpoint if specified (for testing)
    if env.azure_endpoint:
        terraform["provider"]["azurerm"]["features"] = {}
        # Note: endpoint override would go here if needed

    return json.dumps(terraform, indent=2)


def _ingress_fqdn(resources: AzureResources) -> str | None:
    """Terraform reference to the hostname of the externally reachable app."""
    for key, app in resources.azurerm_container_app.items():
        if app.ingress:
            return f"${{azurerm_container_app.{key}.ingress[0].fqdn}}"
    return None


def _build_resources(resources: AzureResources) -> dict[str, Any]:
    """Build the resource section of Terraform JSON."""
    result: dict[str, Any] = {}

    # Add each resource type if it has entries
    if resources.azurerm_container_app_environment:
        result["azurerm_container_app_environment"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_container_app_environment.items()
        }

    if resources.azurerm_container_app:
        result["azurerm_container_app"] = {
            k: _clean_model(v) for k, v in resources.azurerm_container_app.items()
        }

    if resources.azurerm_container_app_job:
        result["azurerm_container_app_job"] = {
            k: _clean_model(v) for k, v in resources.azurerm_container_app_job.items()
        }

    if resources.azurerm_container_registry:
        result["azurerm_container_registry"] = {
            k: _clean_model(v) for k, v in resources.azurerm_container_registry.items()
        }

    if resources.azurerm_postgresql_flexible_server:
        result["azurerm_postgresql_flexible_server"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_postgresql_flexible_server.items()
        }

    if resources.azurerm_postgresql_flexible_server_database:
        result["azurerm_postgresql_flexible_server_database"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_postgresql_flexible_server_database.items()
        }

    if resources.azurerm_mysql_flexible_server:
        result["azurerm_mysql_flexible_server"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_mysql_flexible_server.items()
        }

    if resources.azurerm_mysql_flexible_database:
        result["azurerm_mysql_flexible_database"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_mysql_flexible_database.items()
        }

    if resources.azurerm_private_dns_zone:
        result["azurerm_private_dns_zone"] = {
            k: _clean_model(v) for k, v in resources.azurerm_private_dns_zone.items()
        }

    if resources.azurerm_private_dns_zone_virtual_network_link:
        result["azurerm_private_dns_zone_virtual_network_link"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_private_dns_zone_virtual_network_link.items()
        }

    if resources.azurerm_key_vault:
        result["azurerm_key_vault"] = {
            k: _clean_model(v) for k, v in resources.azurerm_key_vault.items()
        }

    if resources.azurerm_key_vault_secret:
        result["azurerm_key_vault_secret"] = {
            k: _clean_model(v) for k, v in resources.azurerm_key_vault_secret.items()
        }

    if resources.azurerm_user_assigned_identity:
        result["azurerm_user_assigned_identity"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_user_assigned_identity.items()
        }

    if resources.azurerm_role_assignment:
        result["azurerm_role_assignment"] = {
            k: _clean_model(v) for k, v in resources.azurerm_role_assignment.items()
        }

    if resources.azurerm_managed_redis:
        result["azurerm_managed_redis"] = {
            k: _clean_model(v) for k, v in resources.azurerm_managed_redis.items()
        }

    if resources.azurerm_storage_account:
        result["azurerm_storage_account"] = {
            k: _clean_model(v) for k, v in resources.azurerm_storage_account.items()
        }

    if resources.azurerm_storage_container:
        result["azurerm_storage_container"] = {
            k: _clean_model(v) for k, v in resources.azurerm_storage_container.items()
        }

    if resources.azurerm_cdn_frontdoor_profile:
        result["azurerm_cdn_frontdoor_profile"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_cdn_frontdoor_profile.items()
        }

    if resources.azurerm_cdn_frontdoor_endpoint:
        result["azurerm_cdn_frontdoor_endpoint"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_cdn_frontdoor_endpoint.items()
        }

    if resources.azurerm_cdn_frontdoor_origin_group:
        result["azurerm_cdn_frontdoor_origin_group"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_cdn_frontdoor_origin_group.items()
        }

    if resources.azurerm_cdn_frontdoor_origin:
        result["azurerm_cdn_frontdoor_origin"] = {
            k: _clean_model(v)
            for k, v in resources.azurerm_cdn_frontdoor_origin.items()
        }

    if resources.azurerm_cdn_frontdoor_route:
        result["azurerm_cdn_frontdoor_route"] = {
            k: _clean_model(v) for k, v in resources.azurerm_cdn_frontdoor_route.items()
        }

    # Docker resources
    if resources.docker_image:
        result["docker_image"] = {
            k: _clean_model(v) for k, v in resources.docker_image.items()
        }

    if resources.docker_registry_image:
        result["docker_registry_image"] = {
            k: _clean_model(v) for k, v in resources.docker_registry_image.items()
        }

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

    # Remove lifecycle key for now (needs special handling)
    if "lifecycle" in data:
        del data["lifecycle"]

    return data
