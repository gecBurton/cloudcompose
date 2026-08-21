# CI acceptance role (GitHub OIDC → AWS)

One-time setup so the **AWS Acceptance** workflow (`.github/workflows/acceptance.yml`)
can deploy real infrastructure without any long-lived credentials in GitHub.

This Terraform creates:
- a GitHub Actions **OIDC identity provider** in your AWS account,
- an **IAM role** (`cloudcompose-acceptance-ci`) that only `gecBurton/cloudcompose`
  workflows may assume, with `AdministratorAccess` (pragmatic for a sandbox;
  scope down as a follow-up),
- an **S3 bucket** holding Terraform state for the acceptance runs, so that a
  cancelled or timed-out run can still be torn down (see *Recovering a leaked
  run* below), and
- a **DynamoDB lock table** for that bucket, so two runs racing against the
  same `NAME` (or a run racing its own `--destroy-only` recovery) can't
  corrupt each other's state — the same unlocked-S3 race
  `docs/multi-user-state.md` closes for real `cloud-compose` users via
  `backend.aws.dynamodb_table`.

## Apply (once, from a stable connection)

```bash
cd ci
aws-vault exec personal -- terraform init
aws-vault exec personal -- terraform apply
# If the account already has a GitHub OIDC provider:
#   ... terraform apply -var create_oidc_provider=false
```

Copy the `role_arn`, `state_bucket`, and `state_lock_table` outputs.

## Wire it into GitHub

Repo → **Settings → Secrets and variables → Actions → Variables**, add:

| Name | Value |
| --- | --- |
| `AWS_ACCEPTANCE_ROLE_ARN` | the `role_arn` output |
| `AWS_ACCEPTANCE_STATE_BUCKET` | the `state_bucket` output |
| `AWS_ACCEPTANCE_STATE_TABLE` | the `state_lock_table` output |

(They're *variables*, not secrets — none is sensitive, and OIDC means no keys
are stored.)

If `AWS_ACCEPTANCE_STATE_BUCKET` is left unset the workflow still runs, but with
Terraform state on the runner only — a cancelled run then strands its resources
with no way to destroy them. `AWS_ACCEPTANCE_STATE_TABLE` is independently
optional: leaving it unset still uses the state bucket, just without a lock —
safe as long as acceptance runs never actually overlap (enforced today by the
workflow's own `concurrency` group), but worth setting so that guarantee isn't
the only thing standing between two runs and a corrupted state file.

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
STATE_BUCKET=<state_bucket output> STATE_TABLE=<state_lock_table output> NAME=ci42 PROJECT=hello \
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
# Create a resource group for CI resources (the Terraform state
# storage account lives here; not where acceptance runs deploy their
# own infrastructure).
az group create --name cloudcompose-acceptance --location eastus

# Create a service principal for GitHub Actions. Scoped to the whole
# subscription: each acceptance run creates its own resource group
# (ci<run-number>), so a resource-group-scoped role assignment can't
# be created ahead of time.
az ad sp create-for-rbac \
  --name "cloudcompose-acceptance-ci" \
  --role "Contributor" \
  --scopes /subscriptions/{subscription-id}

# Contributor cannot create role assignments (excludes
# Microsoft.Authorization/*/Write). cloudcompose's own generated
# Terraform creates an azurerm_role_assignment (Key Vault Secrets
# User, for the app's managed identity) in every deployed app, which
# needs this grant.
az role assignment create --assignee <service-principal-object-id> \
  --role "Role Based Access Control Administrator" \
  --scope /subscriptions/{subscription-id}

# Contributor's dataActions is empty -- it grants management-plane
# access only. Key Vault's RBAC data plane (getSecret, setSecret) is
# gated by dataActions. Terraform itself creates azurerm_key_vault_secret
# resources and reads them back on refresh, so the CI service
# principal needs its own data-plane grant, separate from the
# app-identity grant above.
az role assignment create --assignee <service-principal-object-id> \
  --role "Key Vault Secrets Officer" \
  --scope /subscriptions/{subscription-id}
```

## Create the Terraform state backend

Acceptance runs keep state in Azure Blob Storage, so a run cancelled mid-flight
leaves state behind that any machine can destroy from. Shared-key access is
disabled: Terraform authenticates to the backend with Entra ID
(`use_azuread_auth`), which means every identity that runs the smoke test — the
CI service principal and each developer — needs **Storage Blob Data
Contributor** on the account. Contributor on the resource group is not enough;
that grants control-plane rights, not data-plane ones.

```bash
SA=cloudcomposeacceptstate   # globally unique, 3-24 lowercase alphanumeric

az storage account create \
  --name "$SA" \
  --resource-group cloudcompose-acceptance \
  --location uksouth \
  --sku Standard_LRS \
  --kind StorageV2 \
  --min-tls-version TLS1_2 \
  --allow-blob-public-access false \
  --allow-shared-key-access false

# Versioning and soft delete make a corrupted or truncated state recoverable.
az storage account blob-service-properties update \
  --account-name "$SA" --resource-group cloudcompose-acceptance \
  --enable-versioning true --enable-delete-retention true --delete-retention-days 30

SCOPE=$(az storage account show -n "$SA" -g cloudcompose-acceptance --query id -o tsv)

# The CI service principal, then yourself.
az role assignment create --assignee <service-principal-object-id> \
  --role "Storage Blob Data Contributor" --scope "$SCOPE"
az role assignment create --assignee "$(az ad signed-in-user show --query id -o tsv)" \
  --role "Storage Blob Data Contributor" --scope "$SCOPE"

# Role assignments take a few seconds to propagate before this succeeds.
az storage container create --name tfstate --account-name "$SA" --auth-mode login
```

If `AZURE_STATE_RESOURCE_GROUP` is left unset the workflow still runs, but with
Terraform state on the runner only — a cancelled run then strands its resources
with no way to destroy them except by hand, and `--destroy-only` cannot help.

## Configure Federated Credentials (OIDC)

In Azure Portal:
1. Go to **Azure AD → App registrations → cloudcompose-acceptance-ci**
2. **Certificates & secrets → Federated credentials**
3. Add credential:
   - **Scenario**: GitHub Actions deploying Azure resources
   - **Organization**: gecBurton
   - **Repository**: cloudcompose
   - **Entity type**: Branch
   - **Branch**: main (and/or pull_request)

## Wire it into GitHub

Repo → **Settings → Secrets and variables → Actions → Variables**, add:

| Name | Value |
| --- | --- |
| `AZURE_CLIENT_ID` | Service principal app ID |
| `AZURE_TENANT_ID` | Azure AD tenant ID |
| `AZURE_SUBSCRIPTION_ID` | Azure subscription ID |
| `AZURE_STATE_RESOURCE_GROUP` | `cloudcompose-acceptance` (or your RG name) |
| `AZURE_STATE_ACCOUNT` | `cloudcomposeacceptstate` (the storage account above) |

## Run

Repo → **Actions → Azure Acceptance → Run workflow** → choose an example.

## Recovering a leaked run

```bash
PROVIDER=azure STATE_RG=cloudcompose-acceptance NAME=ci42 PROJECT=hello \
  az login && scripts/smoke-test.sh --destroy-only
```

## Teardown

```bash
az group delete --name cloudcompose-acceptance --yes
az ad sp delete --id <service-principal-app-id>
```
