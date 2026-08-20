# Multi-User State: Remote Backends and Safe Environment Teardown

## The problem

`cloudcompose` today has no story for more than one person (or CI run,
or laptop) working against the same environment or the same app at the
same time.

`env-<name>/` and `app-<env>-<project>/` each hold an ordinary local
`terraform.tfstate` file (see `docs/authored-environment-config.md`'s
non-goals). "Multiple apps share one environment" is true at the level
of generated *config* — any number of `compile` runs can read the same
environment's facts — but false at the level of *state* the moment two
different checkouts (two laptops, or a laptop and CI) both run
`terraform apply` against `environment.yaml`/`compose.yml` for the same
environment or app. Each gets its own local state file, so each
independently believes it owns that environment's VPC/cluster/ALB. The
second `apply` doesn't merge with the first's state — it either fails
on a naming collision or, worse, silently creates a duplicate. Nothing
detects or prevents this today.

The only remote-backend support that exists at all is
`scripts/smoke-test.sh`'s `write_backend()`, a CI-only convenience that
writes an uncommitted `backend_ci.tf` next to `main.tf.json` — not a
`cloudcompose` feature, and with no state locking (S3: `encrypt = true`
only, no `dynamodb_table`; see `ci/main.tf`).

A related, second-order problem: `cloudcompose down` only ever destroys
a single app's directory, deliberately never the shared environment
(`down.go`'s own doc comment) — the right safety default, but it leaves
tearing an environment down as a bare `terraform destroy` a human runs
by hand, with no warning if other apps still depend on it. Once state
can live in a shared backend, that check becomes possible to build
properly instead of relying on memory.

This doc is scoped to those two things — remote state + locking, and
safe environment teardown — and deliberately does not touch command
naming, the CLI tree, or the binary name. Those are a separate,
independent decision (see "Related but out of scope" below); coupling
a UX rename to a correctness fix would force every existing script, CI
pipeline, and habit to break on the same day as the safety fix, for no
correctness benefit.

## Design

### 1. Remote backend, authored in `environment.yaml`

`InitConfig` (`internal/models/init_config.go`) gains an optional
top-level `Backend` block, mirroring the `aws:`/`azure:`/`gcp:`
discriminated-block pattern already used for provider config:

```yaml
provider: aws
name: prod
backend:
  aws:
    bucket: my-org-tfstate
    region: eu-west-2
    dynamodb_table: my-org-tflocks   # optional; recommended
# ...
```

```yaml
provider: azure
name: prod
backend:
  azure:
    resource_group_name: my-org-tfstate-rg
    storage_account_name: myorgtfstate
    container_name: tfstate
    use_azuread_auth: true   # default true; matches ci/'s own convention
```

```yaml
provider: gcp
name: prod
backend:
  gcp:
    bucket: my-org-tfstate
```

Rules, matching `initconfig.Validate`'s existing discriminated-union
style:

- `backend:` is optional. Omitted entirely → today's behavior (local
  state), with `cloudcompose init` printing a one-line warning: *"No
  backend configured — state is local to this machine. Multiple users
  sharing this environment must configure `backend:` in
  environment.yaml."* Never silently assume a backend; local state must
  stay an explicit, visible choice, not a trap.
- If present, exactly the block matching `provider:` may be set
  (`backend.aws` requires `provider: aws`, etc.) — same strict rule
  `Validate` already applies to the top-level `aws:`/`azure:`/`gcp:`
  blocks.
- `key` is **never authored** — it's always derived from `name` the
  same way every other resource/output-directory name already is
  (`env-<name>` for environments; see "Key derivation" below for apps).
  This keeps `environment.yaml` free of the one field that's
  mechanically implied by `name` already, and prevents a copy-pasted
  `environment.yaml` (e.g. `prod` → `staging`) from silently pointing
  two different environments at the same state key by accident.

`initconfig.Load`'s `knownTopLevelKeys` gains `"backend"`;
`initconfig.Validate` gains the discriminated-block check above, plus:
GCP's `dynamodb_table`-equivalent doesn't exist (GCS locking is native,
no separate resource), so only AWS's block has an optional
`dynamodb_table` field — its absence is allowed but `cloudcompose init`
warns the same way it does for no backend at all, since unlocked S3
state has the exact same concurrent-apply race this whole doc exists to
close.

### 2. Emitting the backend block

Each `Generate*Environment` function
(`internal/compiler/{aws,azure,gcp}/environment_generator.go`) already
builds `terraform := map[string]any{"required_version": ...,
"required_providers": ...}` as a plain, untyped map — see
`terraform_json.go`'s `MarshalIndentedJSON`, which round-trips the
whole manifest through `map[string]any` for deterministic key
ordering. No model or serialization changes are needed: a backend block
is just another key in that same map.

```go
if cfg.Backend != nil && cfg.Backend.AWS != nil {
    terraform["backend"] = map[string]any{
        "s3": map[string]any{
            "bucket":         cfg.Backend.AWS.Bucket,
            "key":            backendKeyForEnvironment(cfg.Name),
            "region":         cfg.Backend.AWS.Region,
            "encrypt":        true,
            "dynamodb_table": cfg.Backend.AWS.DynamoDBTable, // omitted if empty
        },
    }
}
```

`Generate*Environment` signatures don't need a new parameter for this —
`Backend` already arrives as a field on the same `*models.InitConfig`
they're already passed.

### 3. Apps get backends too, derived the same way

Apps (`cloudcompose up`/`cloudcompose down`) read their environment's
facts via `LoadEnvironment(envDir)` (`internal/compiler/environment.go`),
which already shells out to `terraform output -json` in `envDir` — a
call that works identically whether that directory's state is local or
remote, since it only ever talks to Terraform's own CLI, never a state
file directly (`terraform_outputs.go`'s own doc comment already notes
this). `LoadEnvironment`'s return value is the natural place to also
surface *that same environment's backend config* for the app to reuse:
an app's state must live in the same backend as its environment's (one
bucket/account per org, not one per app), just under a different key.

Add a `Backend *models.BackendConfig` field to each `Load*Environment`
call path — surfaced via a **new** `output "backend"` block in
`environment_generator.go`, parallel to the existing `output
"environment"` block (`aws/environment_generator.go:304-309`) — so
`compileApp` can write the app's own `main.tf.json` with:

```go
if env.Backend != nil {
    terraform["backend"] = map[string]any{
        "s3": map[string]any{
            "bucket": env.Backend.AWS.Bucket,
            "key":    backendKeyForApp(envName, projectName),
            "region": env.Backend.AWS.Region,
            "encrypt": true,
            "dynamodb_table": env.Backend.AWS.DynamoDBTable,
        },
    }
}
```

This makes the backend a property of the *environment*, inherited by
every app compiled against it — not a separate decision each app
author makes, which would risk different apps in the same environment
scattering their state across inconsistent buckets/regions.

#### Key derivation

Matching `ResourceNamer`'s `env.Name-app.Name-...` convention and
`smoke-test.sh`'s existing `acceptance/<NAME>/...` shape:

```go
func backendKeyForEnvironment(envName string) string {
    return fmt.Sprintf("cloudcompose/%s/environment.tfstate", envName)
}
func backendKeyForApp(envName, projectName string) string {
    return fmt.Sprintf("cloudcompose/%s/%s.tfstate", envName, projectName)
}
```

No flag or config field exposes these — like `env-<name>`/
`app-<env>-<project>` output directory naming, key derivation is
mechanical and non-configurable, for the same reason: two environments
or apps that happen to share a name must not silently share state, and
nothing about the key should depend on where a command happens to be
run from.

### 4. State locking

- **AWS**: `dynamodb_table` in the backend config (optional but
  strongly recommended; warned about if absent, per above). This is a
  gap in `ci/main.tf` too — no lock table is provisioned there today;
  fixing that is out of scope for this doc but should follow shortly
  after, since CI's own acceptance runs have exactly the same race this
  doc is trying to close for real users.
- **Azure**: `azurerm` backend's blob lease locking is automatic,
  requires no config — already the shape `use_azuread_auth = true`
  produces in `smoke-test.sh`.
- **GCP**: GCS backend locking is automatic (object generation
  preconditions), requires no config.

### 5. Bootstrapping the backend itself

`cloudcompose` never creates the S3 bucket/Azure storage account/GCS
bucket a backend points at — that chicken-and-egg problem (state needs
a bucket; provisioning a bucket is itself infrastructure) is
deliberately out of scope for this feature to solve generically. But
leaving this as a single sentence with no example is a real onboarding
cliff: `ci/main.tf` already contains almost exactly the Terraform a team
needs (S3 bucket + versioning + public-access-block +
server-side-encryption), missing only a lock table. Rather than have
every team reinvent this, add a small `examples/bootstrap-state/`
directory (one `main.tf.json`-generating example per cloud, or a plain
checked-in `.tf` file if that's simpler here since it's a one-time,
manually-applied bootstrap and not something `cloudcompose` itself
generates) that provisions exactly what `backend:` in
`environment.yaml` expects to already exist, including the AWS lock
table `ci/main.tf` is currently missing. Document this in
`docs/authored-environment-config.md` alongside the new `backend:`
field reference, as "run this once per org/account before configuring
`backend:`."

### 6. Safe environment teardown: tracking dependent apps

`down.go`'s own comment already states the underlying fact this
depends on: there is nowhere `down` records which project name it
used, so nothing can enumerate "every app compiled against this
environment" today. Once every app's state lives in the *same backend*
as its environment (§3), under a predictable key prefix
(`cloudcompose/<envName>/*.tfstate`), that enumeration becomes possible
without adding any new state of its own: destroying an environment can
list objects under that prefix (S3 `ListObjectsV2`/azurerm blob
list/GCS list) before proceeding, and treat any key other than the
environment's own `environment.tfstate` as a live dependent app. This
needs no new Terraform resource, no registration step added to
`compose up`, and no extra state for `compile`/`up` to write — the
existing state-key naming convention (§3) already encodes exactly the
information needed; adding a redundant marker resource inside each
app's own state would duplicate that with nothing to justify the extra
moving part.

- **Behavior**: if dependent app state keys exist, environment teardown
  refuses by default, listing the offending project names (recovered
  from each key's own filename, no need to open the state itself) and
  suggesting `cloudcompose down` for each first. `--force` skips the
  check entirely (documented as exactly that — a deliberate override,
  matching `--auto-approve`'s own framing in `up.go`/`down.go`'s doc
  comments).
- **No-backend fallback**: without a configured backend there is no
  prefix to list — teardown can't know what depends on it, so it
  behaves exactly as today (a bare `terraform destroy` a human runs by
  hand, with the usual interactive plan/confirm), plus a warning
  identical in spirit to §1's local-state warning: *"No backend
  configured — cannot check for dependent apps. Confirm none exist
  before continuing."*
- **Stale registrations**: an app directory can be deleted locally
  (`rm -rf`, an abandoned checkout) without ever running `down`, leaving
  its state key behind forever and permanently blocking teardown short
  of `--force`. There must be an explicit way to clear this that
  doesn't require trusting `--force` blindly: at minimum, a documented
  "resolve a stale registration" procedure — re-point `compose down`'s
  `--env`/`--project` at the environment and project name recovered
  from the stale key (both are literally the key's own path segments,
  so no separate lookup is needed) to run a normal `terraform destroy`
  against it, or, if the underlying infrastructure is already gone,
  delete the state object directly. This should be spelled out as
  precisely as `ci/README.md`'s existing `--destroy-only` recovery
  procedure for leaked CI runs, which is the closest existing analog.
- **IAM footprint**: listing objects under a prefix (`s3:ListBucket`,
  storage-account list, GCS list) is a broader permission than a
  Terraform backend strictly needs on its own (many locked-down orgs
  scope backend access per-key, not bucket-wide list). This should be
  validated against a realistic minimal-privilege IAM policy — not
  assumed free — before treating this check as always available; the
  no-backend-style fallback behavior (warn, don't block) is the right
  default if the list call fails on a permissions error, same as if no
  backend were configured at all.
- **GCP dependency cost**: implementing the GCS-list call requires
  adding `cloud.google.com/go/storage` (or equivalent) to `go.mod` —
  there is no GCP SDK dependency in this codebase today (AWS and Azure
  SDKs are already present; GCP's own support is deliberately the
  least-verified of the three per `AGENTS.md`). This is a real, if
  small, new dependency to weigh, not an incidental extension of
  something already there.

## Non-goals

- **No automatic backend bootstrap as part of `cloudcompose` itself.**
  §5's example directory is a manually-applied, one-time-per-org
  Terraform project a human runs directly — not something any
  `cloudcompose` command generates or applies.
- **No migration tooling.** Moving an existing local-state
  `env-<name>`/`app-<env>-<project>` directory to a newly-configured
  remote backend is a `terraform init -migrate-state` a human runs by
  hand, same as any other Terraform project; `cloudcompose` doesn't
  wrap it.
- **No cross-environment or cross-app locking beyond what each backend
  already provides natively.** Two people running `cloudcompose init`
  + `terraform apply` against the *same* environment concurrently are
  protected by Terraform's own state lock (once configured); two people
  compiling and upping *different* apps against the same environment
  concurrently were already safe before this doc, since they touch
  disjoint state keys.
- **No UI/registry beyond the dependent-apps listing needed for safe
  environment teardown** — no general `cloudcompose ls`-style command
  to enumerate every app in an environment is proposed here, though the
  same prefix-listing mechanism would make one straightforward to add
  later.

## Related but out of scope

A separate proposal covers renaming the `cloudcompose` binary and
restructuring its CLI into `env`/`compose` subcommand groups (motivated
by reducing how much a human has to type, following Docker Compose's
own UX). That's an independent, purely cosmetic decision from this
doc's correctness fix, and is written up on its own so it can be
decided, scheduled, or dropped without blocking the backend/locking
work here. If both land, this doc's command names (`cloudcompose
init`/`up`/`down`) should be read as whatever that proposal's
equivalents end up being.

## Implementation reference (once built)

- `internal/models/init_config.go` — new `Backend *BackendConfig`
  field; new `BackendConfig`/`AwsBackendConfig`/`AzureBackendConfig`/
  `GcpBackendConfig` structs.
- `internal/compiler/initconfig/initconfig.go` — `knownTopLevelKeys`
  gains `"backend"`; `Validate` gains the discriminated-block +
  optional-lock-table warning checks.
- `internal/compiler/{aws,azure,gcp}/environment_generator.go` — emit
  `terraform["backend"]`; add `output "backend"` alongside the existing
  `output "environment"`.
- `internal/compiler/{aws,azure,gcp}/environment.go` — `Load*Environment`
  decodes the new `backend` output alongside `environment`.
- `internal/compiler/{aws,azure,gcp}/generator.go` — app-level
  generators emit `terraform["backend"]` derived from the loaded
  environment's `Backend` + `backendKeyForApp`.
- New: `internal/compiler/shared/backend_naming.go` —
  `backendKeyForEnvironment`/`backendKeyForApp`.
- New: `internal/compiler/shared/backend_listing.go` — per-cloud "list
  objects under prefix" used by environment teardown's dependent-app
  check; gated entirely behind `Backend` being configured, degrading to
  a warning (not a hard error) if the list call itself fails (missing
  permission, etc).
- New: `examples/bootstrap-state/` — one manually-applied Terraform
  project per cloud provisioning exactly what `backend:` expects to
  already exist (bucket/storage account/GCS bucket + lock table where
  applicable).
- `go.mod` — new GCP storage SDK dependency for the GCS list call (see
  §6's "GCP dependency cost").
- `docs/authored-environment-config.md` — add `backend:` to the field
  reference table; link to `examples/bootstrap-state/`.
- `ci/main.tf` — add the missing `aws_dynamodb_table` lock table
  (tracked as a following fix, not blocking this doc, but noted since
  CI's own state has the identical unlocked-S3 gap this doc closes for
  users).
