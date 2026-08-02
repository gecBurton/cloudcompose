# CI acceptance role (GitHub OIDC → AWS)

One-time setup so the **AWS Acceptance** workflow (`.github/workflows/acceptance.yml`)
can deploy real infrastructure without any long-lived credentials in GitHub.

This Terraform creates:
- a GitHub Actions **OIDC identity provider** in your AWS account,
- an **IAM role** (`composey-acceptance-ci`) that only `gecBurton/composey`
  workflows may assume, with `AdministratorAccess` (pragmatic for a sandbox;
  scope down as a follow-up), and
- an **S3 bucket** holding Terraform state for the acceptance runs, so that a
  cancelled or timed-out run can still be torn down (see *Recovering a leaked
  run* below).

## Apply (once, from a stable connection)

```bash
cd ci
aws-vault exec personal -- terraform init
aws-vault exec personal -- terraform apply
# If the account already has a GitHub OIDC provider:
#   ... terraform apply -var create_oidc_provider=false
```

Copy the `role_arn` and `state_bucket` outputs.

## Wire it into GitHub

Repo → **Settings → Secrets and variables → Actions → Variables**, add both:

| Name | Value |
| --- | --- |
| `AWS_ACCEPTANCE_ROLE_ARN` | the `role_arn` output |
| `AWS_ACCEPTANCE_STATE_BUCKET` | the `state_bucket` output |

(They're *variables*, not secrets — neither is sensitive, and OIDC means no keys
are stored.)

If `AWS_ACCEPTANCE_STATE_BUCKET` is left unset the workflow still runs, but with
Terraform state on the runner only — a cancelled run then strands its resources
with no way to destroy them.

## Run

Repo → **Actions → AWS Acceptance → Run workflow** → choose an example
(`hello`, `minio-s3`, `build-webapp`, or `doctor`). The job assumes the role,
deploys the example, asserts it, and tears everything down — pass or fail.

## If a run cannot assume the role

AWS reports only `Not authorized to perform sts:AssumeRoleWithWebIdentity`,
never which condition failed. The workflow's **Debug OIDC claims** step prints
the `sub` GitHub actually sent; compare it with `github_subject_patterns` in
`variables.tf` and add the observed form if it differs.

This repository issues *ID-qualified* subjects — `repo:owner@1234/repo@5678:...`
rather than `repo:owner/repo:...` — so a trust policy written only against the
plain form is rejected even though everything else is correct.

## Recovering a leaked run

The smoke test tears everything down via a shell trap, and the workflow bounds
itself with `timeout-minutes` and a `concurrency` group. If a run still dies
without cleaning up (runner failure, force-cancel), its state survives in the
state bucket under `acceptance/<NAME>/` and can be destroyed from anywhere.
`NAME` is `ci<run_number>`, shown in the failed job's log.

```bash
STATE_BUCKET=<state_bucket output> NAME=ci42 PROJECT=hello \
  aws-vault exec personal -- scripts/smoke-test.sh --destroy-only
```

That always tears down the bootstrap environment (the VPC, NAT gateway and ALB
— the expensive part). The app stack additionally needs its generated manifest,
so if `build/<PROJECT>/main.tf.json` is not present, recompile it first with the
same compose file and project name; the command prints the exact invocation.

## Teardown of this role

```bash
cd ci && aws-vault exec personal -- terraform destroy
```

---

# CI acceptance role (GitHub OIDC → Azure)

Similar setup for the **Azure Acceptance** workflow (`.github/workflows/azure-acceptance.yml`).

## Prerequisites

- Azure CLI installed and logged in: `az login`
- An Azure subscription

## Create Azure Service Principal for OIDC

```bash
# Create a resource group for CI resources
az group create --name composey-acceptance --location eastus

# Create a service principal for GitHub Actions
az ad sp create-for-rbac \
  --name "composey-acceptance-ci" \
  --role "Contributor" \
  --scopes /subscriptions/{subscription-id}/resourceGroups/composey-acceptance
```

## Configure Federated Credentials (OIDC)

In Azure Portal:
1. Go to **Azure AD → App registrations → composey-acceptance-ci**
2. **Certificates & secrets → Federated credentials**
3. Add credential:
   - **Scenario**: GitHub Actions deploying Azure resources
   - **Organization**: gecBurton
   - **Repository**: composey
   - **Entity type**: Branch
   - **Branch**: main (and/or pull_request)

## Wire it into GitHub

Repo → **Settings → Secrets and variables → Actions → Variables**, add:

| Name | Value |
| --- | --- |
| `AZURE_CLIENT_ID` | Service principal app ID |
| `AZURE_TENANT_ID` | Azure AD tenant ID |
| `AZURE_SUBSCRIPTION_ID` | Azure subscription ID |
| `AZURE_STATE_RESOURCE_GROUP` | `composey-acceptance` (or your RG name) |

## Run

Repo → **Actions → Azure Acceptance → Run workflow** → choose an example.

## Recovering a leaked run

```bash
STATE_RG=composey-acceptance NAME=ci42 PROJECT=hello \
  az login && scripts/smoke-test-azure.sh --destroy-only
```

## Teardown

```bash
az group delete --name composey-acceptance --yes
az ad sp delete --id <service-principal-app-id>
```
