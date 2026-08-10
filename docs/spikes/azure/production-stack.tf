# SPIKE — hand-written target output for examples/production-stack on Azure.
# Not generated, not deployed. Schema is close to azurerm but not verified.
#
# Exercises: cdn + waf, autoscaling, a scheduled task, RDS and ElastiCache
# substitution, and relationship-derived connectivity. AWS emits 33 resources.

data "azurerm_resource_group" "env" {
  name = "prod"
}

data "azurerm_container_app_environment" "env" {
  name                = "prod-cae"
  resource_group_name = data.azurerm_resource_group.env.name
}

# Platform-owned networking for private database access. Azure requires a
# delegated subnet and a private DNS zone per server family; there is no direct
# analogue of an RDS subnet group.
data "azurerm_subnet" "db" {
  name                 = "postgres"
  virtual_network_name = "prod-vnet"
  resource_group_name  = data.azurerm_resource_group.env.name
}

data "azurerm_private_dns_zone" "postgres" {
  name                = "prod.postgres.database.azure.com"
  resource_group_name = data.azurerm_resource_group.env.name
}

# --- db: postgres -> PostgreSQL Flexible Server ------------------------------
resource "random_password" "db" {
  length = 20
}

resource "azurerm_key_vault_secret" "db" {
  name         = "prod-production-stack-db-credentials"
  value        = random_password.db.result
  key_vault_id = data.azurerm_key_vault.env.id

  lifecycle {
    ignore_changes = [value]
  }
}

data "azurerm_key_vault" "env" {
  name                = "prod-kv"
  resource_group_name = data.azurerm_resource_group.env.name
}

resource "azurerm_postgresql_flexible_server" "db" {
  name                = "prod-production-stack-db"
  resource_group_name = data.azurerm_resource_group.env.name
  location            = data.azurerm_resource_group.env.location
  version             = "16"

  administrator_login    = "cloudcompose"
  administrator_password = random_password.db.result

  # size: small -> burstable tier. The size vocabulary maps cleanly here.
  sku_name   = "B_Standard_B1ms"
  storage_mb = 32768

  delegated_subnet_id = data.azurerm_subnet.db.id
  private_dns_zone_id = data.azurerm_private_dns_zone.postgres.id
}

# --- cache: redis -> Azure Cache for Redis -----------------------------------
resource "azurerm_redis_cache" "cache" {
  name                = "prod-production-stack-cache"
  resource_group_name = data.azurerm_resource_group.env.name
  location            = data.azurerm_resource_group.env.location

  capacity             = 0
  family               = "C"
  sku_name             = "Basic"
  non_ssl_port_enabled = false
  minimum_tls_version  = "1.2"
}

# --- web: the public service -------------------------------------------------
resource "azurerm_user_assigned_identity" "web" {
  name                = "prod-production-stack-web"
  resource_group_name = data.azurerm_resource_group.env.name
  location            = data.azurerm_resource_group.env.location
}

# The named-tier equivalent of an inline IAM policy: Azure ships built-in RBAC
# roles, so there is no policy document to synthesise.
resource "azurerm_role_assignment" "web_kv" {
  scope                = data.azurerm_key_vault.env.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_user_assigned_identity.web.principal_id
}

resource "azurerm_container_app" "web" {
  name                         = "prod-production-stack-web"
  resource_group_name          = data.azurerm_resource_group.env.name
  container_app_environment_id = data.azurerm_container_app_environment.env.id
  revision_mode                = "Single"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.web.id]
  }

  secret {
    name                = "db-password"
    key_vault_secret_id = azurerm_key_vault_secret.db.id
    identity            = azurerm_user_assigned_identity.web.id
  }

  template {
    # min_scale / max_scale map directly.
    min_replicas = 2
    max_replicas = 10

    container {
      name = "web"
      # size: medium -> 1024 cpu / 2048 MiB.
      image  = "nginx:latest"
      cpu    = 1.0
      memory = "2Gi"

      env {
        name  = "REDIS_URL"
        value = "redis://${azurerm_redis_cache.cache.hostname}:${azurerm_redis_cache.cache.ssl_port}"
      }

      env {
        name  = "DB_HOST"
        value = azurerm_postgresql_flexible_server.db.fqdn
      }

      env {
        name        = "DB_PASSWORD"
        secret_name = "db-password"
      }
    }

    # AWS target-tracking becomes a KEDA scale rule. Same intent, different
    # engine: KEDA polls a metric and Container Apps has no notion of separate
    # scale-in/scale-out cooldowns.
    custom_scale_rule {
      name             = "cpu"
      custom_rule_type = "cpu"
      metadata = {
        type  = "Utilization"
        value = "70"
      }
    }

    custom_scale_rule {
      name             = "memory"
      custom_rule_type = "memory"
      metadata = {
        type  = "Utilization"
        value = "80"
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

# --- cdn: CloudFront + WAF -> Front Door + WAF policy ------------------------
resource "azurerm_cdn_frontdoor_profile" "web" {
  name                = "prod-production-stack-web-fd"
  resource_group_name = data.azurerm_resource_group.env.name
  sku_name            = "Premium_AzureFrontDoor" # WAF managed rules need Premium
}

resource "azurerm_cdn_frontdoor_endpoint" "web" {
  name                     = "prod-production-stack-web"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.web.id
}

resource "azurerm_cdn_frontdoor_origin_group" "web" {
  name                     = "web"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.web.id

  load_balancing {}
}

resource "azurerm_cdn_frontdoor_origin" "web" {
  name                          = "web"
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.web.id

  # The Container App's own FQDN is the origin — the analogue of the
  # data.aws_lb lookup the AWS backend needs for an ALB origin.
  host_name                      = azurerm_container_app.web.ingress[0].fqdn
  origin_host_header             = azurerm_container_app.web.ingress[0].fqdn
  https_port                     = 443
  http_port                      = 80
  certificate_name_check_enabled = true
  enabled                        = true
}

resource "azurerm_cdn_frontdoor_route" "web" {
  name                          = "default"
  cdn_frontdoor_endpoint_id     = azurerm_cdn_frontdoor_endpoint.web.id
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.web.id
  cdn_frontdoor_origin_ids      = [azurerm_cdn_frontdoor_origin.web.id]

  supported_protocols    = ["Http", "Https"]
  patterns_to_match      = ["/*"]
  forwarding_protocol    = "HttpsOnly"
  https_redirect_enabled = true
}

resource "azurerm_cdn_frontdoor_firewall_policy" "web" {
  name                = "prodproductionstackwebwaf"
  resource_group_name = data.azurerm_resource_group.env.name
  sku_name            = azurerm_cdn_frontdoor_profile.web.sku_name
  enabled             = true
  mode                = "Prevention"

  # Equivalent of AWSManagedRulesCommonRuleSet.
  managed_rule {
    type    = "Microsoft_DefaultRuleSet"
    version = "2.1"
    action  = "Block"
  }
}

resource "azurerm_cdn_frontdoor_security_policy" "web" {
  name                     = "prod-production-stack-web-waf"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.web.id

  security_policies {
    firewall {
      cdn_frontdoor_firewall_policy_id = azurerm_cdn_frontdoor_firewall_policy.web.id

      association {
        patterns_to_match = ["/*"]

        domain {
          cdn_frontdoor_domain_id = azurerm_cdn_frontdoor_endpoint.web.id
        }
      }
    }
  }
}

# NOTE: no region pinning. The AWS backend must create a CLOUDFRONT-scoped WAF
# in us-east-1 through an aliased provider; Front Door WAF policies live in the
# application's own resource group.

# --- cleanup: scheduled task -------------------------------------------------
resource "azurerm_container_app_job" "cleanup" {
  name                         = "prod-production-stack-cleanup"
  resource_group_name          = data.azurerm_resource_group.env.name
  location                     = data.azurerm_resource_group.env.location
  container_app_environment_id = data.azurerm_container_app_environment.env.id

  replica_timeout_in_seconds = 1800
  replica_retry_limit        = 1

  schedule_trigger_config {
    # LEAK: the semantic model carries EventBridge syntax. The compose file says
    # `cron(0 2 * * ? *)`; Azure needs standard 5-field cron `0 2 * * *`. The
    # AWS-only day-of-week `?` placeholder has no meaning here.
    cron_expression = "0 2 * * *"
  }

  template {
    container {
      name    = "cleanup"
      image   = "busybox"
      cpu     = 0.25
      memory  = "0.5Gi"
      command = ["echo", "cleaning up..."]
    }
  }
}

# No EventBridge role, no ecs:RunTask policy, no PassRole: the schedule is a
# property of the job rather than a separate rule invoking a task definition.

# --- Relationships -----------------------------------------------------------
# AWS derives four security group rules from the compose `depends_on` graph.
# There is no equivalent here: apps in a Container Apps environment reach each
# other over internal DNS with no per-pair enforcement point. The database and
# cache are reachable because they sit on the delegated subnet, which is an
# environment-level decision, not a service-pair one. See README.md — this is
# the single biggest modelling mismatch the spike found.
