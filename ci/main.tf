terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region
}

locals {
  oidc_host = "token.actions.githubusercontent.com"
}

# GitHub Actions OIDC identity provider. Skip creation (look it up instead) if
# the account already has one — an account can only have a single provider per URL.
resource "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 1 : 0

  url             = "https://${local.oidc_host}"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["1c58a3a8518e8759bf075b76b750d4f2df264fcd"]
}

data "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 0 : 1

  url = "https://${local.oidc_host}"
}

locals {
  oidc_provider_arn = var.create_oidc_provider ? aws_iam_openid_connect_provider.github[0].arn : data.aws_iam_openid_connect_provider.github[0].arn
}

# The role GitHub Actions in this repo may assume via OIDC. The trust is scoped
# to the repo by matching the token's `sub` claim; tighten the patterns to a
# branch or environment for extra safety if desired.
resource "aws_iam_role" "acceptance" {
  name = var.role_name

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = local.oidc_provider_arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = { "${local.oidc_host}:aud" = "sts.amazonaws.com" }
        # A list is OR-ed: any one pattern matching is enough.
        StringLike = { "${local.oidc_host}:sub" = var.github_subject_patterns }
      }
    }]
  })
}

# The smoke test provisions VPC/ECS/RDS/ElastiCache/S3/ECR/IAM/SecretsManager/ELB.
# AdministratorAccess is pragmatic for a sandbox test account; scoping this down
# to least-privilege is a worthwhile follow-up.
resource "aws_iam_role_policy_attachment" "admin" {
  role       = aws_iam_role.acceptance.name
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}

data "aws_caller_identity" "current" {}

# Remote state for the acceptance runs themselves.
#
# Terraform state on a CI runner is ephemeral: if a run is cancelled, the runner
# dies, or the job times out, the state file is lost along with it and the VPC,
# NAT gateway and ALB it created can no longer be reached by `terraform destroy`.
# They then bill indefinitely until someone deletes them by hand. Keeping state
# here means any leaked run stays destroyable from anywhere.
resource "aws_s3_bucket" "state" {
  bucket = "${var.role_name}-state-${data.aws_caller_identity.current.account_id}"

  # State for throwaway acceptance environments; nothing here is worth keeping
  # once the role itself is torn down.
  force_destroy = true
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

# Expire old run state so the bucket does not accumulate objects forever.
resource "aws_s3_bucket_lifecycle_configuration" "state" {
  bucket = aws_s3_bucket.state.id

  rule {
    id     = "expire-old-run-state"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = 30
    }

    expiration {
      days = 90
    }
  }
}
