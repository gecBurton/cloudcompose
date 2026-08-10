# SPIKE — hand-written target output for examples/doctor on GCP.
# Not generated, not deployed. Schema is close to the google provider but not verified.
#
# Exercises: build-from-source, minio -> object storage, and
# startup_grace_period. This is the example the AWS acceptance run proves.

locals {
  project  = "prod-cloudcompose"
  region   = "europe-west2"
  app_name = "doctor"
}

data "google_compute_network" "env" {
  name    = "prod-vpc"
  project = local.project
}

# --- build: ECR -> Artifact Registry -----------------------------------------
# Platform-owned, like ACR: one repository holds many images, so cloudcompose has no
# per-service repository to create.
data "google_artifact_registry_repository" "env" {
  project       = local.project
  location      = local.region
  repository_id = "prod-images"
}

resource "docker_image" "doctor" {
  name = "${local.region}-docker.pkg.dev/${local.project}/${data.google_artifact_registry_repository.env.repository_id}/prod-doctor-doctor:latest"

  build {
    context  = "app"
    platform = "linux/amd64"
  }
}

resource "docker_registry_image" "doctor" {
  name          = docker_image.doctor.name
  keep_remotely = true
}

# --- blobs: minio -> Cloud Storage -------------------------------------------
resource "google_storage_bucket" "blobs" {
  name     = "prod-doctor-blobs"
  project  = local.project
  location = local.region

  uniform_bucket_level_access = true
  force_destroy               = true
}

# --- db: postgres -> Cloud SQL -----------------------------------------------
resource "random_password" "db" {
  length = 20
}

resource "google_secret_manager_secret" "db" {
  project   = local.project
  secret_id = "prod-doctor-db-credentials"

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
  name             = "prod-doctor-db"
  project          = local.project
  region           = local.region
  database_version = "POSTGRES_16"

  settings {
    tier = "db-f1-micro"

    ip_configuration {
      ipv4_enabled    = false
      private_network = data.google_compute_network.env.id
    }
  }

  deletion_protection = false
}

resource "google_sql_user" "db" {
  name     = "cloudcompose"
  project  = local.project
  instance = google_sql_database_instance.db.name
  password = random_password.db.result
}

# --- cache: redis -> Memorystore ---------------------------------------------
resource "google_redis_instance" "cache" {
  name               = "prod-doctor-cache"
  project            = local.project
  region             = local.region
  tier               = "BASIC"
  memory_size_gb     = 1
  authorized_network = data.google_compute_network.env.id
}

# --- doctor: the application -------------------------------------------------
resource "google_service_account" "doctor" {
  project    = local.project
  account_id = "prod-doctor-doctor"
}

# Predefined roles again replace synthesised policy documents, and the binding
# is per-resource rather than attached to the principal.
resource "google_storage_bucket_iam_member" "doctor_blobs" {
  bucket = google_storage_bucket.blobs.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.doctor.email}"
}

resource "google_secret_manager_secret_iam_member" "doctor_db" {
  project   = local.project
  secret_id = google_secret_manager_secret.db.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.doctor.email}"
}

resource "google_artifact_registry_repository_iam_member" "doctor_pull" {
  project    = local.project
  location   = local.region
  repository = data.google_artifact_registry_repository.env.repository_id
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.doctor.email}"
}

resource "google_cloud_run_v2_service" "doctor" {
  name     = "prod-doctor-doctor"
  project  = local.project
  location = local.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    service_account = google_service_account.doctor.email

    scaling {
      min_instance_count = 1
      max_instance_count = 1
    }

    vpc_access {
      network_interfaces {
        network = data.google_compute_network.env.id
      }
      egress = "PRIVATE_RANGES_ONLY"
    }

    containers {
      image = docker_registry_image.doctor.name

      resources {
        limits = {
          cpu    = "250m"
          memory = "512Mi"
        }
      }

      # Cloud Run injects PORT and routes to exactly one port. cloudcompose's
      # `port` maps, but a service exposing several ports cannot be expressed.
      ports {
        container_port = 80
      }

      env {
        name  = "BUCKET_NAME"
        value = google_storage_bucket.blobs.name
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

      env {
        name  = "REDIS_URL"
        value = "redis://${google_redis_instance.cache.host}:${google_redis_instance.cache.port}"
      }

      # startup_grace_period: 120 -> a startup probe whose failure budget covers
      # the same window. Same approximation as Azure, and a better fit than the
      # ECS name suggested: Cloud Run genuinely has a startup concept.
      startup_probe {
        http_get {
          path = "/health"
          port = 80
        }
        period_seconds    = 10
        failure_threshold = 12
      }

      liveness_probe {
        http_get {
          path = "/health"
          port = 80
        }
        period_seconds = 30
      }
    }
  }
}

resource "google_cloud_run_v2_service_iam_member" "doctor_public" {
  project  = local.project
  location = local.region
  name     = google_cloud_run_v2_service.doctor.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# ALTERNATIVE worth noting: the idiomatic Cloud Run route to Cloud SQL is not a
# private IP at all but the built-in connector, which mounts a unix socket:
#
#   volumes {
#     name = "cloudsql"
#     cloud_sql_instance {
#       instances = [google_sql_database_instance.db.connection_name]
#     }
#   }
#
# The app then connects to /cloudsql/<connection_name> — a socket path, not a
# host. See README.md: this is the finding that matters most here, because
# cloudcompose's endpoint injection assumes a database endpoint is a hostname.
