variable "region" {
  type        = string
  description = "AWS region for the acceptance role/provider."
  default     = "eu-west-2"
}

variable "github_repo" {
  type        = string
  description = "owner/repo allowed to assume the role via OIDC. Documentation only; the trust is enforced by github_subject_patterns."
  default     = "gecBurton/cloudcompose"
}

variable "github_subject_patterns" {
  type        = list(string)
  description = <<-EOT
    Patterns matched (OR-ed) against the OIDC token's `sub` claim.

    This repository issues ID-qualified subjects, embedding the numeric owner
    and repository IDs: `repo:owner@1234/repo@5678:ref:refs/heads/main`. The
    plain `repo:owner/repo:*` form therefore never matches on its own, which
    presents as `Not authorized to perform sts:AssumeRoleWithWebIdentity`.
    Both forms are listed so the trust survives that setting being toggled.

    The ID-qualified form is the stronger of the two: the IDs are immutable, so
    it keeps pointing at this exact repository even if it is renamed, and it
    cannot be claimed by someone who registers the old name. Keep the patterns
    fully qualified — a wildcard in the owner or repo segment would let an
    unrelated repository with a similar name assume this role.

    If a run fails to assume the role, the workflow's "Debug OIDC claims" step
    prints the `sub` that GitHub actually sent; match it here.
  EOT
  default = [
    "repo:gecBurton@8233643/cloudcompose@1305607063:*",
    "repo:gecBurton/cloudcompose:*",
  ]
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
