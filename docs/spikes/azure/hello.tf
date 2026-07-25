# SPIKE — hand-written target output for examples/hello on Azure.
# Not generated, not deployed. Schema is close to azurerm but not verified.
#
# AWS emits 11 resources for this example: security group, egress rule, log
# group, task role, execution role, exec log policy, task definition, ECS
# service, target group, listener rule, ALB ingress rule.
#
# Azure emits one.

# --- Environment (platform-owned, looked up rather than created) -------------
# The AzureEnvironment equivalent of vpc_id / ecs_cluster_arn / alb_arn.
data "azurerm_resource_group" "env" {
  name = "prod"
}

data "azurerm_container_app_environment" "env" {
  name                = "prod-cae"
  resource_group_name = data.azurerm_resource_group.env.name
}

# --- Application -------------------------------------------------------------
resource "azurerm_container_app" "web" {
  name                         = "prod-hello-web"
  resource_group_name          = data.azurerm_resource_group.env.name
  container_app_environment_id = data.azurerm_container_app_environment.env.id
  revision_mode                = "Single"

  template {
    min_replicas = 1
    max_replicas = 1

    container {
      name = "web"
      # size: small -> the smallest valid Container Apps cpu/memory pair.
      image  = "nginxdemos/hello:plain-text"
      cpu    = 0.25
      memory = "0.5Gi"
    }
  }

  # Replaces the entire target group + listener rule + security group triad.
  # Container Apps allocates an FQDN and terminates TLS itself; there is no
  # shared load balancer to attach to and no path-based rule to write.
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

# No log group: the Container Apps environment already owns a Log Analytics
# workspace and every app writes to it. `retention_in_days` becomes a property
# of the environment, i.e. platform-owned, not per-service.

# No roles: with no secrets, no registry and no managed services, this app needs
# no identity at all. AWS still requires an execution role to write logs.
