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
                    "version": "~> 3.0",
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
            "azurerm": {
                "features": {},
            },
            "docker": {},
            "random": {},
        },
        "data": {
            "azurerm_client_config": {
                "current": {},
            },
        },
        "resource": _build_resources(resources),
    }

    # Add custom endpoint if specified (for testing)
    if env.azure_endpoint:
        terraform["provider"]["azurerm"]["features"] = {}
        # Note: endpoint override would go here if needed

    return json.dumps(terraform, indent=2)


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

    if resources.azurerm_redis_cache:
        result["azurerm_redis_cache"] = {
            k: _clean_model(v) for k, v in resources.azurerm_redis_cache.items()
        }

    if resources.azurerm_storage_account:
        result["azurerm_storage_account"] = {
            k: _clean_model(v) for k, v in resources.azurerm_storage_account.items()
        }

    if resources.azurerm_storage_container:
        result["azurerm_storage_container"] = {
            k: _clean_model(v) for k, v in resources.azurerm_storage_container.items()
        }

    if resources.azurerm_cdn_profile:
        result["azurerm_cdn_profile"] = {
            k: _clean_model(v) for k, v in resources.azurerm_cdn_profile.items()
        }

    if resources.azurerm_cdn_endpoint:
        result["azurerm_cdn_endpoint"] = {
            k: _clean_model(v) for k, v in resources.azurerm_cdn_endpoint.items()
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

    # Remove lifecycle key for now (needs special handling)
    if "lifecycle" in data:
        del data["lifecycle"]

    return data
