# Backend bootstrap: Azure (Storage Account + Blob Container)

Provisions the storage account and blob container `backend.azure:` in
`environment.yaml` expects to already exist (see
`docs/authored-environment-config.md`'s "Sharing one environment across
multiple users" and `docs/multi-user-state.md`). `cloud-compose` never
creates this itself -- it's a one-time, manually-applied setup per
organization/subscription, run before any `environment.yaml` references
it.

This mirrors `ci/README.md`'s own hand-run `az storage account create`
steps for the acceptance test suite's state account, expressed as
Terraform instead of a shell script so it's a `terraform apply` like
everything else in this directory, and so it's reviewable/repeatable the
same way.

Unlike S3, azurerm's own blob-lease locking is automatic and needs no
separate lock-table resource -- see `docs/multi-user-state.md`'s
"State locking" section.

## Usage

```bash
cd examples/bootstrap-state/azure
terraform init
terraform apply -var="name=myorg" -var="resource_group_name=myorg-tfstate-rg"
```

Then grant **Storage Blob Data Contributor** on the storage account to
every identity that will run `cloud-compose init`/`up`/`compile`/`down`/
`env-destroy` against a backend-configured environment -- shared-key
access is disabled below (`allow_shared_key_access = false`), so
Terraform's own `azurerm` backend authenticates via Entra ID
(`use_azuread_auth`), which needs this role, not just Contributor on the
resource group (see `ci/README.md`'s identical note: Contributor grants
control-plane rights, not the data-plane rights blob access needs):

```bash
SCOPE=$(az storage account show -n myorgtfstate -g myorg-tfstate-rg --query id -o tsv)
az role assignment create --assignee <your-object-id> \
  --role "Storage Blob Data Contributor" --scope "$SCOPE"
```

Then reference the storage account/container it created in
`environment.yaml`:

```yaml
backend:
  azure:
    resource_group_name: myorg-tfstate-rg
    storage_account_name: myorgtfstate
    container_name: tfstate
    use_azuread_auth: true
```

## What this does not do

- Does not grant any role assignments -- see the manual step above.
  This is deliberately left as a manual, per-identity step (this file
  provisions the shared resource; who gets access to it is an
  org-specific decision, and differs for the CI service principal vs.
  each individual developer, exactly as `ci/README.md`'s own two-role-
  assignment example shows).
- Does not delete anything on `terraform destroy` by default -- no
  `force_destroy`-equivalent is set here; this account holds real,
  non-disposable environment/app state.
