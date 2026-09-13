# Authored Environment Configuration

`environment.yaml` is the authored, reviewable input for
`cloud-compose init` — the environment-config equivalent of
`docker-compose.yml`. Two things are cleanly separated:

| | What | Lifecycle |
|---|---|---|
| **Input** | `environment.yaml` | Authored, committed, reviewed |
| **Output** | Terraform's own state, read live via `terraform output -json` | Never a file Cloud Compose Compiler writes; always current by construction |

`cloud-compose init` reads `environment.yaml` as its **only** input — no
decision flags. To change a decision, edit the file and re-run `init`.

`cloud-compose compile -e <environment-directory>` reads the environment's
facts by running `terraform output -json` in that directory (which must
already have `terraform apply` run in it) and decoding its `environment`
output. Multiple apps can `compile` against the same environment
directory — each reads the same, already-resolved facts and bakes them
in independently; there's no limit on how many apps share one
environment this way, which is the main practical reason to use a shared
environment at all (fewer NAT Gateways/ALBs paid for, rather than one
per app).

`cloud-compose compile --environment <environment.yaml>` is the portable
alternative to `-e`: given the same authored `environment.yaml` (which
must declare a real remote `backend:`, not `local` — see "Sharing one
environment across multiple users" below), it resolves the environment
directly,
without needing to already know where a previous `env init`/`env up`
wrote its output directory. See `docs/deployment-identity-design.md`
for the design rationale — `-e` still works as before.

## Evaluating without a live environment: `--demo`

`cloud-compose compile -d <cloud>` (`aws`/`azure`/`gcp`) generates the same
Terraform JSON a real compile would, using a built-in synthetic
environment with plausible-looking placeholder resource IDs instead of
reading a real one — for a prospective user to see what their compose
file becomes on a given cloud without first running `cloud-compose init`
or holding any cloud credentials at all.

`-e`, `--environment`, and `-d` are mutually exclusive and exactly one is
required: there is no default when none is given, the same "one way to
configure, not two" reasoning `init`'s own flag set follows. The output
is genuinely valid Terraform JSON (every demo environment is checked
against the real provider schema via `terraform validate`), but it is not
deployable as-is — the placeholder IDs (`vpc-demo...`, fake ARNs, etc.)
don't correspond to anything real. `cloud-compose compile` prints a
stderr banner saying so whenever `-d` is used.

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
(`AGENTS.md`, `models/compose.go`'s `XCloud.UnmarshalJSON`). The same
strict rule applies to the required `backend:` block below.

Real, `terraform validate`-checked examples for all three clouds exist
at `examples/hello/environment.yaml` (AWS),
`examples/hello/environment.azure.yaml`, and
`examples/hello/environment.gcp.yaml`.

## Sharing one environment across multiple users: `backend:`

Everything above describes a single environment's *config*. Two people
(or a laptop and CI) both running `terraform apply` against the same
`environment.yaml` is a different concern — *state* — since local state
gives each its own ordinary local `terraform.tfstate`, and the second
`apply` doesn't merge with the first's: it either fails on a naming
collision or silently creates a duplicate.

`backend:` is required, authored alongside the common envelope above.
It's either the bare value `local` (state stays on this machine, the
single-developer/evaluation case) or a mapping configuring a real
Terraform remote backend (with locking) for both the environment and
every app compiled against it:

```yaml
# environment.yaml (local state -- single developer, evaluation)
provider: aws
name: dev
region: eu-west-2
aws:
  vpc_cidr: 10.0.0.0/16
backend: local
```

```yaml
# environment.yaml (AWS example, with a remote backend:)
provider: aws
name: prod
region: eu-west-2
aws:
  vpc_cidr: 10.0.0.0/16
backend:
  aws:
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
  azure:
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
  gcp:
    bucket: my-org-tfstate
```

`backend:` has no default and cannot be omitted: `local` is an
authored, visible choice rather than a silent one (there is no longer a
"no backend configured" warning at `init` time -- there's nothing left
to warn about once every environment.yaml states its choice outright).
If AWS's `backend.aws` is configured without `dynamodb_table`, `init`
still warns about that — unlocked S3 state has the same concurrent-apply
race as `local`.

`local` is also why `cloud-compose compile --environment <file>` (see
`docs/deployment-identity-design.md`) refuses `backend: local`
specifically, distinct from refusing a missing `backend:` altogether:
local state genuinely has no durable locator to resolve from
`environment.yaml` alone, so that command requires a real remote
backend even though `local` is otherwise a perfectly valid choice for
`env init`/`env up`.

The state *key* (S3's `key`, azurerm's `key`, GCS's `prefix`) is never
authored here — it's always derived mechanically from `name:` (for the
environment) or the compose file's own top-level `name:` (for each app
compiled against it — see docs/deployment-identity-design.md), the
same way `env-<name>`/`app-<env>-<project>` output directory names are
never authored either. This is why environment `name:` and every
compose file's own `name:` are restricted to letters, digits,
underscores, and hyphens: an unrestricted name could otherwise be
crafted to collide with a different environment's or app's own backend
key.

`backend:` (when not `local`) assumes the bucket/storage account/lock
table it points at already exists — `cloud-compose` never provisions
one itself (the same chicken-and-egg reason most infra tools don't:
state needs a bucket, provisioning a bucket is itself infrastructure).
See `examples/bootstrap-state/` for a ready-to-copy, manually-applied
Terraform project that provisions exactly what each cloud's `backend:`
block expects, one time per organization/account, before any
`environment.yaml` references it.

Tearing down a shared environment (as opposed to a single app —
`cloud-compose down`) is `cloud-compose env-destroy`: unlike `down`, it
first checks (when `backend:` is a real remote backend, not `local`)
whether any app still depends on the environment — every app compiled
against a backend-configured environment shares that same backend,
under its own key — and refuses by default if any are found, naming
them and suggesting `cloud-compose down` for each first. `--force`
skips that check. See `docs/multi-user-state.md` for the full design
(locking details per cloud, the dependent-app check's own IAM footprint
and degrade-to-warning behavior, and how to resolve a stale
registration left behind by a deleted app directory that never ran
`down`).

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
| **required** — `local`, or a mapping with exactly one of `aws:`/`azure:`/`gcp:` | see "Sharing one environment across multiple users" above |
| `backend.aws.bucket`, `backend.aws.region` | required if `backend.aws` is present |
| `backend.aws.dynamodb_table` | optional, but `init` warns if absent |
| `backend.azure.resource_group_name`, `backend.azure.storage_account_name`, `backend.azure.container_name` | required if `backend.azure` is present |
| `backend.azure.use_azuread_auth` | optional; defaults to `true` |
| `backend.gcp.bucket` | required if `backend.gcp` is present |

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
docs/deployment-identity-design.md for why). Deploying the same
codebase more than once within one environment needs a second compose
file with its own `name:`. `app-<env>-<project>` pairs with `init`'s
own `env-<name>`, naming both halves of one deployment consistently.

## Known gap: GCP CDN inference

`domain` exists on `GcpEnvironment` and flows through to the generated
output, but `gcp/infer.go`'s CDN/load-balancer inference is still a
documented no-op — the field exists so inference isn't blocked on a
schema change once it's built, not because anything consumes it yet.

## Non-goals

- `cloud-compose init`/`compile` themselves still never run `terraform
  apply`/`destroy` or manage Terraform state — they only ever write
  `main.tf.json`. `cloud-compose up`/`down`/`env-destroy` are the
  exceptions: `up` orchestrates `init` + `terraform apply` + `compile` +
  `terraform apply` for the common one-app-one-environment case, `down`
  runs `terraform destroy` against a single already-compiled app's own
  directory (never the shared environment), and `env-destroy` runs
  `terraform destroy` against the shared environment itself (see
  "Sharing one environment across multiple users" above). Every
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
- `cmd/cloudcompose/environment_resolve.go` — `resolveEnvironmentByDefinition`,
  reached via `compile`/`compose up`'s `--environment <environment.yaml>`
  flag: resolves an environment directly from its authored config,
  without the caller needing to already know its generated output
  directory (requires `backend:`; see
  `docs/deployment-identity-design.md`).
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
  `ListDependentApps`, the per-cloud listing `env-destroy`'s safety
  check uses.
- `internal/compiler/{aws,azure,gcp}/environment.go` —
  `Load*Environment(dir)` calls `TerraformOutputs`/
  `OptionalTerraformOutputs` and decodes directly.
- See `docs/multi-user-state.md` for the full design behind all of the
  above.
- CI note: `scripts/ci-environment.{aws,azure}.yaml` are shared,
  committed environment configs; `scripts/smoke-test.sh` substitutes a
  per-run `name:`/`region:` into a generated copy under `build/` before
  passing it to `cloud-compose init -e`, since a single committed file
  can't express a unique name per CI run.
