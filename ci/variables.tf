variable "region" {
  type        = string
  description = "AWS region for the acceptance role/provider."
  default     = "eu-west-2"
}

variable "github_repo" {
  type        = string
  description = "owner/repo allowed to assume the role via OIDC."
  default     = "gecBurton/composey"
}

variable "role_name" {
  type        = string
  description = "Name of the IAM role GitHub Actions assumes."
  default     = "composey-acceptance-ci"
}

variable "create_oidc_provider" {
  type        = bool
  description = "Create the GitHub OIDC provider. Set false if the account already has one."
  default     = true
}
