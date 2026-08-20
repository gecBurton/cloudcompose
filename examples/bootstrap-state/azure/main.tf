terraform {
  required_version = ">= 1.5"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
  }
}

variable "name" {
  description = "Org identifier, used to name the storage account (must be globally unique, 3-24 lowercase alphanumeric characters -- e.g. \"myorg\" produces \"myorgtfstate\")."
  type        = string
}

variable "resource_group_name" {
  description = "Resource group to create the storage account in."
  type        = string
}

variable "location" {
  description = "Azure region for the resource group and storage account."
  type        = string
  default     = "eastus"
}

provider "azurerm" {
  features {}
}

resource "azurerm_resource_group" "state" {
  name     = var.resource_group_name
  location = var.location
}

# Terraform state for every environment/app backend: configures against
# this account -- see docs/multi-user-state.md's key-naming convention
# ("cloudcompose/<env>/environment.tfstate",
# "cloudcompose/<env>/apps/<project>.tfstate"). One account per
# organization/subscription, shared across every environment, not one
# per environment: the key namespace already keeps them apart.
resource "azurerm_storage_account" "state" {
  name                     = "${var.name}tfstate"
  resource_group_name      = azurerm_resource_group.state.name
  location                 = azurerm_resource_group.state.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
  min_tls_version          = "TLS1_2"

  # Shared-key access is disabled -- every backend: block this bootstrap
  # supports authenticates via Entra ID (use_azuread_auth) instead, which
  # means every identity that runs cloudcompose against a
  # backend-configured environment needs Storage Blob Data Contributor
  # on this account (see this directory's own README.md for the role
  # assignment command -- deliberately left as a manual, per-identity
  # step, not provisioned here).
  shared_access_key_enabled = false

  blob_properties {
    # Versioning and soft delete make a corrupted or truncated state
    # object recoverable, mirroring ci/README.md's own
    # `--enable-versioning true --enable-delete-retention true` setup.
    versioning_enabled = true
    delete_retention_policy {
      days = 30
    }
  }
}

# The container `backend.azure.container_name:` points at.
resource "azurerm_storage_container" "state" {
  name                  = "tfstate"
  storage_account_id    = azurerm_storage_account.state.id
  container_access_type = "private"
}

output "resource_group_name" {
  description = "Value for backend.azure.resource_group_name in environment.yaml."
  value       = azurerm_resource_group.state.name
}

output "storage_account_name" {
  description = "Value for backend.azure.storage_account_name in environment.yaml."
  value       = azurerm_storage_account.state.name
}

output "container_name" {
  description = "Value for backend.azure.container_name in environment.yaml."
  value       = azurerm_storage_container.state.name
}
