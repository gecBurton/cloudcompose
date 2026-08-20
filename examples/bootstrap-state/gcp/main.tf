terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

variable "project_id" {
  description = "GCP project ID to create the bucket in."
  type        = string
}

variable "name" {
  description = "Org identifier, used to name the bucket (e.g. \"my-org\" produces \"my-org-tfstate\")."
  type        = string
}

variable "region" {
  description = "GCP region for the bucket."
  type        = string
  default     = "us-central1"
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# Terraform state for every environment/app backend: configures against
# this bucket -- see docs/multi-user-state.md's key-naming convention
# ("cloudcompose/<env>/environment.tfstate",
# "cloudcompose/<env>/apps/<project>.tfstate"). One bucket per
# organization/project, shared across every environment, not one per
# environment: the key namespace already keeps them apart.
resource "google_storage_bucket" "state" {
  name     = "${var.name}-tfstate"
  location = var.region

  # Real, non-disposable environment/app state -- destroying this
  # bucket accidentally must not silently succeed just because it still
  # has objects in it.
  force_destroy = false

  # Recover from a corrupted or half-written state object.
  versioning {
    enabled = true
  }

  uniform_bucket_level_access = true

  public_access_prevention = "enforced"
}

output "bucket" {
  description = "Value for backend.gcp.bucket in environment.yaml."
  value       = google_storage_bucket.state.name
}
