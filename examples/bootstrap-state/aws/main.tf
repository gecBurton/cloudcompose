terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

variable "name" {
  description = "Org/account identifier, used to name the bucket and lock table (e.g. \"my-org\")."
  type        = string
}

variable "region" {
  description = "AWS region for the bucket and lock table."
  type        = string
  default     = "eu-west-2"
}

provider "aws" {
  region = var.region
}

# Terraform state for every environment/app backend: configures against
# this bucket -- see docs/multi-user-state.md's key-naming convention
# ("cloudcompose/<env>/environment.tfstate",
# "cloudcompose/<env>/apps/<project>.tfstate"). One bucket per
# organization/account, shared across every environment, not one per
# environment: the key namespace already keeps them apart.
resource "aws_s3_bucket" "state" {
  bucket = "${var.name}-tfstate"

  # Unlike ci/main.tf's own throwaway acceptance-run state, this bucket
  # holds real, non-disposable environment/app state -- destroying it
  # accidentally (or via a stray `terraform destroy` here) must not
  # silently succeed just because it still has objects in it.
  force_destroy = false
}

# Recover from a corrupted or half-written state object.
resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "state" {
  bucket = aws_s3_bucket.state.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  bucket = aws_s3_bucket.state.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# The lock table `backend.aws.dynamodb_table:` points at --
# `cloud-compose env init` warns if a configured AWS backend omits this, since
# unlocked S3 state has the same concurrent-apply race as no backend at
# all (see docs/multi-user-state.md). PAY_PER_REQUEST: lock traffic is
# proportional to how often humans/CI run `terraform apply`/`destroy`
# against these environments, not a steady load worth provisioning fixed
# capacity for.
resource "aws_dynamodb_table" "locks" {
  name         = "${var.name}-tflocks"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }
}

output "bucket" {
  description = "Value for backend.aws.bucket in environment.yaml."
  value       = aws_s3_bucket.state.bucket
}

output "dynamodb_table" {
  description = "Value for backend.aws.dynamodb_table in environment.yaml."
  value       = aws_dynamodb_table.locks.name
}
