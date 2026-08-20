# Backend bootstrap: AWS (S3 + DynamoDB lock table)

Provisions the S3 bucket and DynamoDB lock table `backend.aws:` in
`environment.yaml` expects to already exist (see
`docs/authored-environment-config.md`'s "Sharing one environment across
multiple users" and `docs/multi-user-state.md`). `cloudcompose` never
creates this itself -- it's a one-time, manually-applied setup per
organization/account, run before any `environment.yaml` references it.

This mirrors `ci/main.tf`'s own state bucket (versioning, public-access
block, server-side encryption), with the one thing that file is
currently missing: a DynamoDB lock table, so concurrent `terraform
apply`/`destroy` runs against the same environment are actually
protected by a state lock, not just given a bucket to race over.

## Usage

```bash
cd examples/bootstrap-state/aws
terraform init
terraform apply -var="name=my-org"
```

Then reference the bucket/table it created in `environment.yaml`:

```yaml
backend:
  aws:
    bucket: my-org-tfstate
    region: eu-west-2
    dynamodb_table: my-org-tflocks
```

## What this does not do

- Does not create IAM policies granting access to the bucket/table --
  every identity that runs `cloudcompose init`/`up`/`compile`/`down`/
  `env-destroy` against a backend-configured environment needs
  `s3:GetObject`/`PutObject`/`DeleteObject` on the bucket and
  `dynamodb:GetItem`/`PutItem`/`DeleteItem` on the table (Terraform's
  own backend requirements), plus `s3:ListBucket` scoped to the
  `cloudcompose/` prefix specifically if `env-destroy`'s dependent-app
  check is to work without falling back to a warning (see
  `docs/multi-user-state.md`'s "IAM footprint" note -- a locked-down org
  may reasonably withhold that last one).
- Does not delete anything on `terraform destroy` here by default
  (`force_destroy = false`, unlike `ci/main.tf`'s own throwaway state
  bucket) -- this bucket holds real, non-disposable environment/app
  state, not ephemeral CI runs.
