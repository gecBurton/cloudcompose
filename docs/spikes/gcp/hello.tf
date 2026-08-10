# SPIKE — hand-written target output for examples/hello on GCP.
# Not generated, not deployed. Schema is close to the google provider but not verified.
#
# AWS emits 11 resources for this example. Azure emits 1. GCP emits 2.

# --- Environment (platform-owned, looked up rather than created) -------------
# The GcpEnvironment equivalent of vpc_id / ecs_cluster_arn / alb_arn. GCP has
# no cluster to join: Cloud Run is regional and scoped by project alone.
locals {
  project  = "prod-cloudcompose"
  region   = "europe-west2"
  env_name = "prod"
}

resource "google_cloud_run_v2_service" "web" {
  name     = "prod-hello-web"
  project  = local.project
  location = local.region

  # Public. Cloud Run allocates an HTTPS run.app URL and terminates TLS itself,
  # so there is no target group, listener rule or security group to write.
  ingress = "INGRESS_TRAFFIC_ALL"

  template {
    scaling {
      min_instance_count = 1
      max_instance_count = 1
    }

    containers {
      image = "nginxdemos/hello:plain-text"

      # size: small -> 256 CPU units / 512 MiB.
      resources {
        limits = {
          cpu    = "250m"
          memory = "512Mi"
        }
      }

      ports {
        container_port = 80
      }
    }
  }
}

# Unauthenticated access is an explicit IAM grant rather than a property of the
# service. This is the same mechanism that makes service-to-service calls
# enforceable — see production-stack.tf.
resource "google_cloud_run_v2_service_iam_member" "web_public" {
  project  = local.project
  location = local.region
  name     = google_cloud_run_v2_service.web.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# No log group: Cloud Logging ingests stdout automatically and retention is a
# project-level policy, so `log_retention_days` has no per-application meaning
# here either.

# No service account: Cloud Run uses the default compute SA when none is given,
# and this app needs no permissions. Contrast AWS, which requires an execution
# role purely to write logs.
