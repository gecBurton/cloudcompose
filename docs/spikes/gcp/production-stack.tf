# SPIKE — hand-written target output for examples/production-stack on GCP.
# Not generated, not deployed. Schema is close to the google provider but not verified.
#
# Exercises: cdn + waf, autoscaling, a schedule, database and cache
# substitution, and relationship-derived connectivity. AWS emits 33 resources.

locals {
  project  = "prod-composey"
  region   = "europe-west2"
  env_name = "prod"
  app_name = "production-stack"
}

# Platform-owned networking. Cloud SQL and Memorystore on private IP both need
# a VPC with private services access already configured.
data "google_compute_network" "env" {
  name    = "prod-vpc"
  project = local.project
}

# --- db: postgres -> Cloud SQL ----------------------------------------------
resource "random_password" "db" {
  length = 20
}

resource "google_secret_manager_secret" "db" {
  project   = local.project
  secret_id = "prod-production-stack-db-credentials"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "db" {
  secret      = google_secret_manager_secret.db.id
  secret_data = random_password.db.result

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_sql_database_instance" "db" {
  name             = "prod-production-stack-db"
  project          = local.project
  region           = local.region
  database_version = "POSTGRES_16"

  settings {
    # size: small. The size vocabulary maps cleanly; unlike Azure Container
    # Apps, GCP imposes no ceiling that `large` would breach.
    tier = "db-f1-micro"

    ip_configuration {
      ipv4_enabled    = false
      private_network = data.google_compute_network.env.id
    }
  }

  deletion_protection = false
}

resource "google_sql_user" "db" {
  name     = "composey"
  project  = local.project
  instance = google_sql_database_instance.db.name
  password = random_password.db.result
}

# --- cache: redis -> Memorystore --------------------------------------------
resource "google_redis_instance" "cache" {
  name               = "prod-production-stack-cache"
  project            = local.project
  region             = local.region
  tier               = "BASIC"
  memory_size_gb     = 1
  authorized_network = data.google_compute_network.env.id
}

# --- web: the public service -------------------------------------------------
resource "google_service_account" "web" {
  project    = local.project
  account_id = "prod-production-stack-web"
}

resource "google_secret_manager_secret_iam_member" "web_db" {
  project   = local.project
  secret_id = google_secret_manager_secret.db.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.web.email}"
}

resource "google_cloud_run_v2_service" "web" {
  name     = "prod-production-stack-web"
  project  = local.project
  location = local.region

  # Behind a load balancer, so only the LB may reach it directly.
  ingress = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"

  template {
    service_account = google_service_account.web.email

    # min_scale / max_scale map directly.
    scaling {
      min_instance_count = 2
      max_instance_count = 10
    }

    vpc_access {
      network_interfaces {
        network = data.google_compute_network.env.id
      }
      egress = "PRIVATE_RANGES_ONLY"
    }

    containers {
      image = "nginx:latest"

      # size: medium -> 1024 CPU units / 2048 MiB.
      resources {
        limits = {
          cpu    = "1"
          memory = "2Gi"
        }
      }

      ports {
        container_port = 80
      }

      env {
        name  = "REDIS_URL"
        value = "redis://${google_redis_instance.cache.host}:${google_redis_instance.cache.port}"
      }

      env {
        name  = "DB_HOST"
        value = google_sql_database_instance.db.private_ip_address
      }

      env {
        name = "DB_PASSWORD"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.db.secret_id
            version = "latest"
          }
        }
      }
    }
  }
}

# NOTE: there is no CPU-target or memory-target scaling policy to write. Cloud
# Run autoscales on request concurrency and a fixed CPU utilisation target that
# is not configurable. The AWS backend's 70%/80% targets are its own invention,
# not something the semantic model carries — which is why this maps cleanly.

# --- cdn: CloudFront + WAF -> external ALB + Cloud CDN + Cloud Armor ---------
# The heaviest of the three clouds: AWS needs 2 resources, Azure 5, GCP 7.
resource "google_compute_region_network_endpoint_group" "web" {
  name                  = "prod-production-stack-web-neg"
  project               = local.project
  region                = local.region
  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = google_cloud_run_v2_service.web.name
  }
}

resource "google_compute_security_policy" "web" {
  name    = "prod-production-stack-web-waf"
  project = local.project

  # Equivalent of AWSManagedRulesCommonRuleSet / Microsoft_DefaultRuleSet.
  rule {
    action   = "deny(403)"
    priority = 1000

    match {
      expr {
        expression = "evaluatePreconfiguredWaf('cve-canary')"
      }
    }
  }

  rule {
    action   = "allow"
    priority = 2147483647

    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
  }
}

resource "google_compute_backend_service" "web" {
  name            = "prod-production-stack-web"
  project         = local.project
  protocol        = "HTTPS"
  enable_cdn      = true
  security_policy = google_compute_security_policy.web.id

  backend {
    group = google_compute_region_network_endpoint_group.web.id
  }
}

resource "google_compute_url_map" "web" {
  name            = "prod-production-stack-web"
  project         = local.project
  default_service = google_compute_backend_service.web.id
}

resource "google_compute_managed_ssl_certificate" "web" {
  name    = "prod-production-stack-web"
  project = local.project

  managed {
    # A custom domain is mandatory: a managed certificate cannot be issued for
    # an anonymous IP. AWS and Azure both hand out a usable default hostname.
    domains = ["production-stack.example.com"]
  }
}

resource "google_compute_target_https_proxy" "web" {
  name             = "prod-production-stack-web"
  project          = local.project
  url_map          = google_compute_url_map.web.id
  ssl_certificates = [google_compute_managed_ssl_certificate.web.id]
}

resource "google_compute_global_forwarding_rule" "web" {
  name       = "prod-production-stack-web"
  project    = local.project
  target     = google_compute_target_https_proxy.web.id
  port_range = "443"
}

# --- cleanup: scheduled task -------------------------------------------------
resource "google_cloud_run_v2_job" "cleanup" {
  name     = "prod-production-stack-cleanup"
  project  = local.project
  location = local.region

  template {
    template {
      containers {
        image   = "busybox"
        command = ["echo", "cleaning up..."]

        resources {
          limits = {
            cpu    = "250m"
            memory = "512Mi"
          }
        }
      }
    }
  }
}

resource "google_service_account" "cleanup_invoker" {
  project    = local.project
  account_id = "prod-production-stack-cleanup-inv"
}

resource "google_cloud_run_v2_job_iam_member" "cleanup_invoker" {
  project  = local.project
  location = local.region
  name     = google_cloud_run_v2_job.cleanup.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.cleanup_invoker.email}"
}

resource "google_cloud_scheduler_job" "cleanup" {
  name    = "prod-production-stack-cleanup"
  project = local.project
  region  = local.region

  # Standard 5-field cron, exactly as the semantic model now stores it. This is
  # the case that justifies the neutral schedule model: no wrapper, no year
  # field, no '?' placeholder.
  schedule  = "0 2 * * *"
  time_zone = "Etc/UTC"

  http_target {
    http_method = "POST"
    uri         = "https://${local.region}-run.googleapis.com/apis/run.googleapis.com/v1/namespaces/${local.project}/jobs/${google_cloud_run_v2_job.cleanup.name}:run"

    oauth_token {
      service_account_email = google_service_account.cleanup_invoker.email
    }
  }
}

# --- Relationships -----------------------------------------------------------
# Unlike Azure, GCP *does* have a per-pair enforcement point: a Cloud Run
# service is private by default and every caller needs an explicit
# roles/run.invoker binding for its own service account. A `Relationship`
# client -> server therefore compiles to exactly one IAM member binding, the
# direct analogue of the AWS security group rule. Reaching Cloud SQL and
# Memorystore is still a VPC-level concern, as on Azure.
#
# e.g. if web called an internal api service:
#
#   resource "google_cloud_run_v2_service_iam_member" "web_to_api" {
#     name   = google_cloud_run_v2_service.api.name
#     role   = "roles/run.invoker"
#     member = "serviceAccount:${google_service_account.web.email}"
#   }
