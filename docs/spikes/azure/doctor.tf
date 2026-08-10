# SPIKE — hand-written target output for examples/doctor on Azure.
# Not generated, not deployed. Schema is close to azurerm but not verified.
#
# Exercises: build-from-source (ECR), minio -> object storage, and
# health_check_grace_period. This is the example the AWS acceptance run proves,
# so it is the fairest like-for-like comparison.

data "azurerm_resource_group" "env" {
  name = "prod"
}

data "azurerm_container_app_environment" "env" {
  name                = "prod-cae"
  resource_group_name = data.azurerm_resource_group.env.name
}

data "azurerm_key_vault" "env" {
  name                = "prod-kv"
  resource_group_name = data.azurerm_resource_group.env.name
}

# --- build: ECR + docker provider -> Azure Container Registry ----------------
# Platform-owned, as the registry is shared. The AWS backend creates a
# repository per service; ACR is a single registry holding many repositories,
# so "create a repo" has no counterpart — you just push a new image name.
data "azurerm_container_registry" "env" {
  name                = "prodacr"
  resource_group_name = data.azurerm_resource_group.env.name
}

resource "docker_image" "doctor" {
  name = "${data.azurerm_container_registry.env.login_server}/prod-doctor-doctor:latest"

  build {
    context  = "app"
    platform = "linux/amd64"
  }
}

resource "docker_registry_image" "doctor" {
  name          = docker_image.doctor.name
  keep_remotely = true
}

# --- blobs: minio -> Storage Account + Blob Container ------------------------
resource "azurerm_storage_account" "blobs" {
  name                     = "proddoctorblobs"
  resource_group_name      = data.azurerm_resource_group.env.name
  location                 = data.azurerm_resource_group.env.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_storage_container" "blobs" {
  name                  = "blobs"
  storage_account_id    = azurerm_storage_account.blobs.id
  container_access_type = "private"
}

# --- db: postgres -> PostgreSQL Flexible Server ------------------------------
resource "random_password" "db" {
  length = 20
}

resource "azurerm_key_vault_secret" "db" {
  name         = "prod-doctor-db-credentials"
  value        = random_password.db.result
  key_vault_id = data.azurerm_key_vault.env.id

  lifecycle {
    ignore_changes = [value]
  }
}

resource "azurerm_postgresql_flexible_server" "db" {
  name                = "prod-doctor-db"
  resource_group_name = data.azurerm_resource_group.env.name
  location            = data.azurerm_resource_group.env.location
  version             = "16"

  administrator_login    = "cloudcompose"
  administrator_password = random_password.db.result

  sku_name   = "B_Standard_B1ms"
  storage_mb = 32768
}

# --- cache: redis -> Azure Cache for Redis -----------------------------------
resource "azurerm_redis_cache" "cache" {
  name                = "prod-doctor-cache"
  resource_group_name = data.azurerm_resource_group.env.name
  location            = data.azurerm_resource_group.env.location

  capacity             = 0
  family               = "C"
  sku_name             = "Basic"
  non_ssl_port_enabled = false
  minimum_tls_version  = "1.2"
}

# --- doctor: the application ------------------------------------------------
resource "azurerm_user_assigned_identity" "doctor" {
  name                = "prod-doctor-doctor"
  resource_group_name = data.azurerm_resource_group.env.name
  location            = data.azurerm_resource_group.env.location
}

# Built-in RBAC roles replace synthesised policy documents entirely. Note this
# is exactly the "named access tier" idea borrowed from ecs_composex, except
# Azure ships the vocabulary rather than requiring cloudcompose to define it.
resource "azurerm_role_assignment" "doctor_blobs" {
  scope                = azurerm_storage_container.blobs.resource_manager_id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = azurerm_user_assigned_identity.doctor.principal_id
}

resource "azurerm_role_assignment" "doctor_kv" {
  scope                = data.azurerm_key_vault.env.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_user_assigned_identity.doctor.principal_id
}

resource "azurerm_role_assignment" "doctor_acr" {
  scope                = data.azurerm_container_registry.env.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_user_assigned_identity.doctor.principal_id
}

resource "azurerm_container_app" "doctor" {
  name                         = "prod-doctor-doctor"
  resource_group_name          = data.azurerm_resource_group.env.name
  container_app_environment_id = data.azurerm_container_app_environment.env.id
  revision_mode                = "Single"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.doctor.id]
  }

  registry {
    server   = data.azurerm_container_registry.env.login_server
    identity = azurerm_user_assigned_identity.doctor.id
  }

  secret {
    name                = "db-password"
    key_vault_secret_id = azurerm_key_vault_secret.db.id
    identity            = azurerm_user_assigned_identity.doctor.id
  }

  template {
    min_replicas = 1
    max_replicas = 1

    container {
      name   = "doctor"
      image  = docker_registry_image.doctor.name
      cpu    = 0.25
      memory = "0.5Gi"

      env {
        name  = "BUCKET_NAME"
        value = azurerm_storage_container.blobs.name
      }

      # No S3_ENDPOINT analogue is optional here: Azure SDKs need the account
      # endpoint explicitly, so a blob "endpoint" variable is load-bearing
      # rather than nice-to-have as it is on AWS.
      env {
        name  = "BLOB_ENDPOINT"
        value = azurerm_storage_account.blobs.primary_blob_endpoint
      }

      env {
        name  = "DB_HOST"
        value = azurerm_postgresql_flexible_server.db.fqdn
      }

      env {
        name        = "DB_PASSWORD"
        secret_name = "db-password"
      }

      env {
        name  = "REDIS_URL"
        value = "rediss://${azurerm_redis_cache.cache.hostname}:${azurerm_redis_cache.cache.ssl_port}"
      }

      # LEAK: `health_check_grace_period: 120` is an ECS concept — seconds for
      # which the *load balancer* health check is ignored after a task starts.
      # Container Apps has no grace period; the nearest expression is a startup
      # probe whose failure budget covers the same window. Approximate, not
      # equivalent: 120s becomes 12 failures at a 10s period.
      startup_probe {
        transport               = "HTTP"
        port                    = 80
        path                    = "/health"
        interval_seconds        = 10
        failure_count_threshold = 12
      }

      liveness_probe {
        transport        = "HTTP"
        port             = 80
        path             = "/health"
        interval_seconds = 30
      }
    }
  }

  ingress {
    external_enabled = true
    target_port      = 80
    transport        = "auto"

    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }
}
