# Backend bootstrap

One Terraform project per cloud, provisioning exactly what `backend:` in
`environment.yaml` expects to already exist (a bucket, and for AWS a
DynamoDB lock table) -- see
`docs/authored-environment-config.md`'s "Sharing one environment across
multiple users" section and `docs/multi-user-state.md` for the full
design this supports.

Unlike every other example in this repo, these are **not** compiled by
`cloud-compose` -- they're plain, hand-written Terraform meant to be
`terraform apply`'d directly, once per organization/account, before any
`environment.yaml` references the bucket/table/storage account they
create. Nothing here is generated, and nothing here is itself managed
by a `cloud-compose`-created environment.

- [`aws/`](aws/) -- S3 bucket + DynamoDB lock table
- [`azure/`](azure/) -- Storage account + blob container (shared-key
  access disabled; every user authenticates via Entra ID)
- [`gcp/`](gcp/) -- GCS bucket (locking is native; no separate lock
  resource)

Each subdirectory's own `README.md` has the exact commands to run and
the `backend:` block to copy into `environment.yaml` afterwards.

## Why this isn't automated

`cloud-compose` never provisions a backend's own storage itself -- the
same chicken-and-egg reason most infrastructure tools leave this to a
human: Terraform state needs somewhere to live, and provisioning that
somewhere is itself infrastructure that would need its own state.
Rather than leave every team to reinvent this from scratch, these three
projects are the ready-to-copy starting point `docs/multi-user-state.md`
promises exists.
