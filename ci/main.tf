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
# to the repo; tighten `sub` to a branch/environment for extra safety if desired.
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
        StringLike   = { "${local.oidc_host}:sub" = "repo:${var.github_repo}:*" }
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
