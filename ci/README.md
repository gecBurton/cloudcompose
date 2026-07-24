# CI acceptance role (GitHub OIDC → AWS)

One-time setup so the **AWS Acceptance** workflow (`.github/workflows/acceptance.yml`)
can deploy real infrastructure without any long-lived credentials in GitHub.

This Terraform creates:
- a GitHub Actions **OIDC identity provider** in your AWS account, and
- an **IAM role** (`composey-acceptance-ci`) that only `gecBurton/composey`
  workflows may assume, with `AdministratorAccess` (pragmatic for a sandbox;
  scope down as a follow-up).

## Apply (once, from a stable connection)

```bash
cd ci
aws-vault exec personal -- terraform init
aws-vault exec personal -- terraform apply
# If the account already has a GitHub OIDC provider:
#   ... terraform apply -var create_oidc_provider=false
```

Copy the `role_arn` output.

## Wire it into GitHub

Repo → **Settings → Secrets and variables → Actions → Variables → New variable**:
- Name: `AWS_ACCEPTANCE_ROLE_ARN`
- Value: the `role_arn` from above

(It's a *variable*, not a secret — an ARN is not sensitive, and OIDC means no keys
are stored.)

## Run

Repo → **Actions → AWS Acceptance → Run workflow** → choose an example
(`hello`, `minio-s3`, `build-webapp`, or `doctor`). The job assumes the role,
deploys the example, asserts it, and tears everything down — pass or fail.

## Teardown of this role

```bash
cd ci && aws-vault exec personal -- terraform destroy
```
