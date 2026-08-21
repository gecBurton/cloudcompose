# Backend bootstrap: GCP (GCS bucket)

Provisions the GCS bucket `backend.gcp:` in `environment.yaml` expects to
already exist (see `docs/authored-environment-config.md`'s "Sharing one
environment across multiple users" and `docs/multi-user-state.md`).
`cloud-compose` never creates this itself -- it's a one-time,
manually-applied setup per organization/project, run before any
`environment.yaml` references it.

GCS backend locking is automatic (object generation preconditions) and
needs no separate lock resource -- see `docs/multi-user-state.md`'s
"State locking" section. GCP support is otherwise the least-verified of
the three clouds this project targets (see `AGENTS.md`); this bootstrap
config has not been exercised against a real deployment.

## Usage

```bash
cd examples/bootstrap-state/gcp
terraform init
terraform apply -var="project_id=my-gcp-project-id" -var="name=my-org"
```

Then grant **Storage Object Admin** (`roles/storage.objectAdmin`) on the
bucket to every identity that will run `cloud-compose init`/`up`/
`compile`/`down`/`env-destroy` against a backend-configured environment:

```bash
gcloud storage buckets add-iam-policy-binding gs://my-org-tfstate \
  --member="user:you@example.com" \
  --role="roles/storage.objectAdmin"
```

Then reference the bucket it created in `environment.yaml`:

```yaml
backend:
  gcp:
    bucket: my-org-tfstate
```

## What this does not do

- Does not grant any IAM bindings -- see the manual step above,
  deliberately left per-identity rather than provisioned here (the CI
  service account and each developer typically need different
  bindings).
- Does not enable `force_destroy` -- this bucket holds real,
  non-disposable environment/app state, not something safe to delete
  by accident.
