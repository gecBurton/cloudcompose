# Environment Configuration and State

Covers `environment.yaml` (the authored, reviewable input for `cloud-compose
env init`) end to end: its schema, how it's resolved, how state is shared
safely across multiple users/CI, and how a shared environment is torn down.

## Authored input vs. generated output

`environment.yaml` is the environment-config equivalent of
`docker-compose.yml`. Two things are cleanly separated:

| | What | Lifecycle |
|---|---|---|
| **Input** | `environment.yaml` | Authored, committed, reviewed |
| **Output** | Terraform's own state, read live via `terraform output -json` | Never a file Cloud Compose Compiler writes; always current by construction |

`cloud-compose env init` reads `environment.yaml` as its **only** input — no
decision flags. To change a decision, edit the file and re-run `env init`.

`cloud-compose compile -e <environment.yaml>` (equivalently, `--env`)
resolves the environment directly from its authored config: it derives
`<dir of environment.yaml>/env-<name>` (the same directory `env
init`/`env up` themselves write to), reads that directory's Terraform
facts by running `terraform output -json` there, and decodes its
`environment` output. The environment must already be applied
(`cloud-compose env init` + `terraform apply`, or `cloud-compose env
up`) — `compile` never creates or modifies it; environment changes are
a deliberate act, never a side effect of deploying an app. Multiple
apps can `compile` against the same environment — each reads the
same, already-resolved facts and bakes them in independently; there's
no limit on how many apps share one environment this way, which is the
main practical reason to use a shared environment at all (fewer NAT
Gateways/ALBs paid for, rather than one per app).

`-e`/`--env` means the same thing on every command that takes it
(`env init`, `env up`, `compile`, `up`, `down`,
`ps`, `logs`, `env down`): the authored
`environment.yaml`. There is no separate way to point at an
already-applied output directory directly — the directory is always
derived from the file, never authored or passed independently. See
`docs/deployment-model.md` for the design rationale (application/environment
identity, why generated directories are scratch space, and CLI history).

## Schema: common envelope + discriminated provider block

```yaml
# environment.yaml (AWS example)
provider: aws
name: prod
region: eu-west-2
retain_data_on_destroy: true
high_availability_enabled: false
backup_retention_days: 7
log_retention_days: 7
tags:
  Team: platform

aws:
  vpc_cidr: 10.0.0.0/16
  az_count: 2
  create_alb: true
  certificate_arn: null
```

```yaml
# environment.yaml (Azure example)
provider: azure
name: prod
region: eastus
retain_data_on_destroy: true
high_availability_enabled: false
backup_retention_days: 7
log_retention_days: 7
tags: {}

azure:
  vnet_cidr: 10.0.0.0/16
```

```yaml
# environment.yaml (GCP example)
provider: gcp
name: prod
region: us-central1
retain_data_on_destroy: true
tags: {}

gcp:
  vpc_cidr: 10.0.0.0/16
  project_id: my-gcp-project-id   # required
```

**Strict/discriminated, not permissive**: only the block matching
`provider:` may be present. An `azure:` block in a file declaring
`provider: aws` is a validation error — consistent with this codebase's
convention that unknown/mismatched `x-cloud` keys are hard errors
(`AGENTS.md`, `models/compose.go`'s `XCloud.UnmarshalJSON`). The
required `backend:` block below applies the same "don't name the cloud
twice" idea, but expressed differently: `backend.remote:`'s own shape
already depends on `provider:`, so there's no separate `backend.aws:`/
`backend.azure:`/`backend.gcp:` key to mismatch in the first place.

Real, `terraform validate`-checked examples for all three clouds exist
at `examples/hello/environment.yaml` (AWS),
`examples/hello/environment.azure.yaml`, and
`examples/hello/environment.gcp.yaml`.

## Sharing one environment across multiple users: `backend:`

Everything above describes a single environment's *config*. Two people
(or a laptop and CI) both running `terraform apply` against the same
`environment.yaml` is a different concern — *state*. Without a shared
backend, each gets its own ordinary local `terraform.tfstate`, each
independently believing it owns that environment's VPC/cluster/ALB —
the second `apply` doesn't merge with the first, it either fails on a
naming collision or silently creates a duplicate.

The fix has two parts: a required, authored remote backend (state +
locking), and a dependent-app check before an environment can be torn
down.

`backend:` is required, authored alongside the common envelope above.
It's either a `local:` block (state stays on this machine, at an
authored path — the single-developer/evaluation case) or a mapping
configuring a real Terraform remote backend (with locking) for both
the environment and every app compiled against it:

```yaml
# environment.yaml (local state -- single developer, evaluation)
provider: aws
name: dev
region: eu-west-2
aws:
  vpc_cidr: 10.0.0.0/16
backend:
  local:
    path: ./terraform.tfstate   # required; resolved relative to this file's own directory
```

```yaml
# environment.yaml (AWS example, with a remote backend:)
provider: aws
name: prod
region: eu-west-2
aws:
  vpc_cidr: 10.0.0.0/16
backend:
  remote:
    bucket: my-org-tfstate
    region: eu-west-2
    dynamodb_table: my-org-tflocks   # optional, but strongly recommended
```

```yaml
# environment.yaml (Azure example, with a remote backend:)
provider: azure
name: prod
region: eastus
azure:
  vnet_cidr: 10.0.0.0/16
backend:
  remote:
    resource_group_name: my-org-tfstate-rg
    storage_account_name: myorgtfstate
    container_name: tfstate
    use_azuread_auth: true   # default; disables shared-key storage access
```

```yaml
# environment.yaml (GCP example, with a remote backend:)
provider: gcp
name: prod
region: us-central1
gcp:
  vpc_cidr: 10.0.0.0/16
  project_id: my-gcp-project-id
backend:
  remote:
    bucket: my-org-tfstate
```

`backend:` has no default and cannot be omitted: `local:` is an
authored, visible choice rather than a silent one (there is no longer
a "no backend configured" warning at `init` time -- there's nothing
left to warn about once every environment.yaml states its choice
outright). `backend.local.path` is itself required, with no default —
an environment's state file location was otherwise an ambient side
effect of whichever directory `terraform apply` happened to be run in,
not an authored fact. It's always resolved relative to
`environment.yaml`'s own directory, never as an absolute path or
relative to the shell's current directory (the same rule `init`'s
own output-location derivation already follows). `backend.remote:`'s
own shape depends on `provider:` — it never names the cloud a second
time, since `provider:` already does, mirroring how `output "backend"`
decodes the deployed-facts side the same way — see the field reference
below for exactly which fields each provider expects. If AWS's
`backend.remote` is configured without `dynamodb_table`, `init` still
warns about that — unlocked S3 state has the same concurrent-apply
race as `local`.

`cloud-compose compile -e <environment.yaml>` works for `local:` and a
real remote backend equally: `local.path` is authored and resolved
relative to `environment.yaml`'s own directory, so regenerating the
environment's output directory always reconnects to the same state
file — just as deterministic to resolve as a remote backend's derived
state key. The remaining caveat is the same one any local file has:
the state file itself has to still exist on whatever machine/checkout
is running the command.

The state *key* (S3's `key`, azurerm's `key`, GCS's `prefix`) is never
authored — it's always derived mechanically from `name:` (for the
environment) or the compose file's own top-level `name:` (for each app
compiled against it — see `docs/deployment-model.md`), the same way
`env-<name>`/`app-<env>-<project>` output directory names are never
authored either:

```go
func backendKeyForEnvironment(envName string) string {
	return fmt.Sprintf("cloudcompose/%s/environment.tfstate", envName)
}
func backendKeyForApp(envName, projectName string) string {
	return fmt.Sprintf("cloudcompose/%s/%s.tfstate", envName, projectName)
}
```

This prevents a copy-pasted `environment.yaml` (`prod` → `staging`)
from silently pointing two different environments at the same state
key. It's also why environment `name:` and every compose file's own
`name:` are restricted to letters, digits, underscores, and hyphens: an
unrestricted name could otherwise be crafted to collide with a
different environment's or app's own backend key.

The backend is a property of the *environment*, inherited by every app
compiled against it (surfaced via `output "backend"`, decoded by
`Load*Environment` alongside `output "environment"`) — not a separate
decision each app author makes, which would risk different apps in the
same environment scattering their state across inconsistent
buckets/regions.

`backend:` (when not `local`) assumes the bucket/storage account/lock
table it points at already exists — `cloud-compose` never provisions
one itself (the same chicken-and-egg reason most infra tools don't:
state needs a bucket, provisioning a bucket is itself infrastructure).
See `examples/bootstrap-state/` for a ready-to-copy, manually-applied
Terraform project that provisions exactly what each cloud's `backend:`
block expects, one time per organization/account, before any
`environment.yaml` references it.

### State locking

- **AWS**: `dynamodb_table` in the backend config — optional but
  strongly recommended; `cloud-compose env init` warns if absent, since
  unlocked S3 state has the same concurrent-apply race this feature
  closes.
- **Azure**: `azurerm` backend's blob lease locking is automatic, no
  config needed.
- **GCP**: GCS backend locking is automatic (object generation
  preconditions), no config needed.

## Safe environment teardown: dependent-app check

Tearing down a shared environment (as opposed to a single app —
`cloud-compose down`) is `cloud-compose env down`. Every app's state
lives in the same backend as its environment, under a predictable key
prefix (`cloudcompose/<envName>/*.tfstate`), so enumerating "every app
compiled against this environment" needs no new state: `env down`
lists objects under that prefix (S3 `ListObjectsV2`/azurerm blob
list/GCS list, `internal/compiler/{aws,azure,gcp}/backend_listing.go`'s
`ListDependentApps`) and treats any key other than the environment's
own `environment.tfstate` as a live dependent app.

- **Behavior**: unlike `down`, `env down` first checks (when `backend:`
  is a real remote backend, not `local`) whether any app still depends
  on the environment — every app compiled against a backend-configured
  environment shares that same backend, under its own key — and
  refuses by default if any are found, listing the offending project
  names (recovered from each key's own filename) and suggesting
  `cloud-compose down` for each first. `--force` skips the check
  entirely.
- **No-backend fallback**: without a configured backend there is no
  prefix to list — teardown behaves as a bare `terraform destroy`, plus
  a warning that dependent apps can't be checked for.
- **Stale registrations**: an app directory deleted locally (`rm -rf`,
  an abandoned checkout) without running `down` leaves its state key
  behind, permanently blocking teardown short of `--force`. Recovery:
  re-point `down`'s `--env` at the environment with a compose file
  whose top-level `name:` matches the project name recovered from the
  stale key (see `ci/README.md`'s `--destroy-only` for the closest
  existing analog), or delete the state object directly if the
  underlying infrastructure is already gone.
- **IAM footprint**: listing objects under a prefix is a broader
  permission than a Terraform backend strictly needs on its own — many
  locked-down orgs scope backend access per-key, not bucket-wide list.
  The list call degrades to a warning (not a hard error) if it fails on
  a permissions error, same as the no-backend case.

## Field reference

| Common envelope | Notes |
|---|---|
| `provider` | required |
| `name` | required |
| `region` | optional; per-provider default (`eu-west-2`/`eastus`/`us-central1`) |
| `tags` | plain YAML map |
| `retain_data_on_destroy` | default `true` |
| `high_availability_enabled` | default `false`; AWS `multi_az`/Azure `ZoneRedundant`; not wired for GCP (Cloud SQL has its own equivalent settings) |
| `backup_retention_days` | default `7` |
| `log_retention_days` | default `7`; applied uniformly, not per-service. Azure's Log Analytics Workspace has a hard minimum of 30 days — `GenerateAzureEnvironment` clamps up if a lower value is given; AWS keeps whatever value is given |
| `domain` | optional on AWS/Azure (each gets a free CloudFront/Front Door hostname); required for GCP if any service declares `cdn: true` (Google-managed cert requires domain ownership) — not enforced at `init` time, since whether `cdn: true` is used isn't known until `cloud-compose compile` parses the compose file |

| `aws:` block | |
|---|---|
| `vpc_cidr`, `az_count`, `create_alb`, `certificate_arn`, `aws_endpoint` | |

| `azure:` block | |
|---|---|
| `vnet_cidr` | |

| `gcp:` block | |
|---|---|
| `vpc_cidr` | |
| `project_id` | **required** — GCP inference depends on it throughout `gcp/infer.go` |

| `backend:` block | |
|---|---|
| **required** — exactly one of `local:`/`remote:` | see "Sharing one environment across multiple users" above |
| `backend.local.path` | required if `backend.local` is present; resolved relative to environment.yaml's own directory |
| `backend.remote.bucket`, `backend.remote.region` | required if `provider: aws` |
| `backend.remote.dynamodb_table` | optional (aws only), but `init` warns if absent |
| `backend.remote.resource_group_name`, `backend.remote.storage_account_name`, `backend.remote.container_name` | required if `provider: azure` |
| `backend.remote.use_azuread_auth` | optional (azure only); defaults to `true` |
| `backend.remote.bucket` | required if `provider: gcp` |

`init` and `compile` take no output-location flag at all: `init` always
writes to `<dir of -e>/env-<name>`, and `compile` always writes to
`<dir of -f (compose.yml)>/app-<environment name>-<project name>` —
both derived from the input file's own location, not the shell's
current directory, so output never depends on where a command happens
to be run from. `compile`'s output directory name includes both the
environment's name and the project's name so the same compose.yml can
be compiled against more than one environment (e.g. dev and prod)
without one overwriting another's output — every actual Terraform
resource `compile` produces is named `env.Name-app.Name-...` (see e.g.
`aws/infer.go`'s `getName` closure). The project name is the compose
file's own top-level `name:` field, not a flag: there is no
`-p`/`--project` override and no directory-basename fallback, and a
compose file with no `name:` is rejected outright (see
`docs/deployment-model.md` for why). Deploying the same
codebase more than once within one environment needs a second compose
file with its own `name:`. `app-<env>-<project>` pairs with `init`'s
own `env-<name>`, naming both halves of one deployment consistently.

## Known gap: GCP CDN inference

`domain` exists on `GcpEnvironment` and flows through to the generated
output, but `gcp/infer.go`'s CDN/load-balancer inference is still a
documented no-op — the field exists so inference isn't blocked on a
schema change once it's built, not because anything consumes it yet.
See `docs/compiler-design.md` for the rest of GCP's known gaps.

## Non-goals

- `cloud-compose init`/`compile` themselves still never run `terraform
  apply`/`destroy` or manage Terraform state — they only ever write
  `main.tf.json`. `cloud-compose up`/`down`/`env down` are the
  exceptions: `up` orchestrates `init` + `terraform apply` + `compile` +
  `terraform apply` for the common one-app-one-environment case, `down`
  runs `terraform destroy` against a single already-compiled app's own
  directory (never the shared environment), and `env down` runs
  `terraform destroy` against the shared environment itself. Every
  `apply`/`destroy` any of these runs stays interactive by default (no
  `-auto-approve`) — the same plan-review checkpoint a human running the
  steps by hand would see, just without needing to type each command.
  All three offer an opt-in, off-by-default `--auto-approve` for
  non-interactive callers (CI, scripts) that have already decided not
  to have a human review the plan for a given run — see
  `cmd/cloudcompose/env_up.go`'s, `compose_down.go`'s, and `env_down.go`'s own
  doc comments for the reasoning.
- No multi-environment-per-file support (Terraform-workspace-style) —
  one `environment.yaml` = one directory = one environment.
- No reverse-generation tooling to produce a starting `environment.yaml`
  from an already-deployed environment.
- No automatic backend bootstrap — `backend:` assumes its own
  bucket/storage account/lock table already exists; see
  `examples/bootstrap-state/` for the one-time, manually-applied setup
  this assumes.
- **No migration tooling.** Moving an existing local-state directory to
  a newly-configured remote backend is a `terraform init
  -migrate-state` a human runs by hand.
- **No cross-environment or cross-app locking beyond what each backend
  already provides natively.** Two people compiling and upping
  *different* apps against the same environment concurrently were
  already safe before this feature, since they touch disjoint state
  keys.
- **No general `cloud-compose ls`-style registry** beyond the
  dependent-apps listing needed for safe teardown, though the same
  prefix-listing mechanism would make one straightforward to add later.

## Implementation reference

- `internal/models/init_config.go` — `InitConfig`,
  `Aws/Azure/GcpInitConfig`, `BackendConfig`,
  `Aws/Azure/GcpBackendConfig`.
- `internal/compiler/initconfig` — `Load` (reads `environment.yaml`,
  returns `(nil, nil)` if missing), `Validate` (strict/discriminated
  checks, including `backend:`), and `BackendWarnings` (the non-fatal
  "no lock table" warning `cloud-compose init` prints).
- `cmd/cloudcompose/env_init.go` — `-e`/`--env` (default
  `environment.yaml`); no decision flags, no output-location flag.
- `cmd/cloudcompose/environment_resolve.go` — `resolveEnvironmentByDefinition`/
  `environmentDirFromDefinition`, reached via `-e`/`--env` on every
  command that takes an environment: derives `env-<name>` from the
  authored `environment.yaml` and reads it directly, without the
  caller needing to know or pass its generated output directory
  themselves. Never creates or modifies that directory (see
  `docs/deployment-model.md`).
- `cmd/cloudcompose/env_down.go` — `env down`'s dependent-app
  safety check and `--force` escape hatch.
- `internal/compiler/{aws,azure,gcp}/environment_generator.go` — each
  declares a plain `output "environment"` block (and, when `backend:`
  is configured, an `output "backend"` block too); no `local_file`
  resource.
- `internal/compiler/shared/terraform_outputs.go` — `TerraformOutputs`/
  `OptionalTerraformOutputs(dir, outputName)` shell out to `terraform
  output -json`.
- `internal/compiler/shared/terraform_output_decode.go` — decode helpers
  (`ToStringMap`/`ToStringSlice`/`ToStringPtr`).
- `internal/compiler/shared/backend_naming.go`,
  `backend_output_decode.go`, `app_backend_block.go` — backend state key
  derivation (`BackendKeyForEnvironment`/`BackendKeyForApp`), decoding
  the `output "backend"` shape, and building an app's own backend block
  from its environment's.
- `internal/compiler/{aws,azure,gcp}/backend_listing.go` —
  `ListDependentApps`, the per-cloud listing `env down`'s safety
  check uses.
- `internal/compiler/{aws,azure,gcp}/environment.go` —
  `Load*Environment(dir)` calls `TerraformOutputs`/
  `OptionalTerraformOutputs` and decodes directly, including the
  `backend` output.
- `internal/compiler/{aws,azure,gcp}/generator.go` — app-level
  generators emit `terraform["backend"]` derived from the loaded
  environment's `Backend` + `backendKeyForApp`.
- `examples/bootstrap-state/` — one manually-applied Terraform project
  per cloud provisioning what `backend:` expects to already exist
  (bucket/storage account/GCS bucket + lock table where applicable).
- CI note: `scripts/ci-environment.{aws,azure}.yaml` are shared,
  committed environment configs; `scripts/smoke-test.sh` substitutes a
  per-run `name:`/`region:` into a generated copy under `build/` before
  passing it to `cloud-compose init -e`, since a single committed file
  can't express a unique name per CI run.

## Related but out of scope

CLI command naming/tree shape is a separate decision — see
`docs/deployment-model.md`'s "CLI shape" section. This feature's design
didn't depend on it either way.
