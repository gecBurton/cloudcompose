# Design: Authored Environment Configuration

> **Status: implemented (2026-08-08, revised twice since).** Implemented
> three times in nine days:
>
> 1. The first version generated a fact file (`environment.facts.json`)
>    via a Terraform `local_file` resource and included a hash-based
>    drift check against it. Removed the same day in favor of reading
>    Terraform's own live state directly (`terraform output -json`) —
>    see "Revision 1: no fact file" below.
> 2. The second version kept `environment.yaml` as authored input, but
>    let CLI flags override its fields per-invocation. Removed on
>    2026-08-09 in favor of file-only, no decision flags at all — see
>    "Revision 2: no flags either" below.
>
> "What was built" at the end of this doc reflects the current, final
> state after both revisions, not the original design.

## The problem

`docker-compose.yml` is source: authored by a human, versioned, reviewed,
and deterministic to compile. The environment side of Cloud Compose
Compiler had no equivalent. Before this design, "configuring an
environment" meant passing CLI flags to `cloudcompose init` once, which
were never recorded anywhere:

```bash
cloudcompose init --provider aws --name prod --region eu-west-2 \
  --vpc-cidr 10.0.0.0/16 --az-count 2 --create-alb
```

The only artifact that persisted was a generated file (originally
`environment.yml`, later `environment.facts.json`) written by a
Terraform `local_file` resource, made almost entirely of facts that
don't exist until Terraform creates them (a VPC ID, an ALB ARN, a
cluster ARN). Treating it as "the environment config" conflated two
different kinds of information that have no business sharing one file:

- **Decisions** — region, VPC CIDR, AZ count, whether to create an ALB,
  tags. Knowable in advance. Authored by a human. Should be reviewable
  the same way a compose.yml change is reviewable.
- **Facts** — the VPC ID, ALB ARN, cluster ARN Terraform assigns at
  `apply` time. Not knowable in advance. Can only ever be generated
  output, never authored.

This caused real, already-observed problems, not just an aesthetic
complaint:

- `cloudcompose init --provider gcp` generated an environment file missing
  `project_id` — a field GCP inference depends on heavily — because
  nothing validated the generated file's completeness before handing it
  to `cloudcompose main`. Silent until deploy time.
- `--azure-endpoint`/`--gcp-endpoint` were dead flags: declared on the
  CLI, never actually forwarded to the generator. Nothing caught this
  because there was no schema the flags were checked against.
- There was no way to know, looking at a deployed environment's fact
  file, what `init` invocation (or whether a human edited `main.tf.json`
  by hand afterward) produced it.

## The design: authored input, live-read output

Two things, cleanly separated, but only one of them is a *file*:

| | What | Lifecycle |
|---|---|---|
| **Input** | `environment.yaml` | Authored, committed, reviewed — same as `docker-compose.yml` |
| **Output** | Terraform's own state, read live via `terraform output -json` | Never a file Cloud Compose Compiler writes; always current, by construction |

`cloudcompose init` reads `environment.yaml` as its **only** input — there
are no decision flags (see "Revision 2: no flags either" below for why
CLI overrides were removed). To change a decision, edit the file and
re-run `init`.

`cloudcompose main -e <environment-directory>` reads the environment's facts
by running `terraform output -json` in that directory (which must be a
directory `cloudcompose init` generated, with `terraform apply` already run
in it) and decoding its `environment` output. See "Revision 1: no fact
file" below for why this replaced a generated file.

### Schema: common envelope + discriminated provider block

Mirrors the shape the Terraform `output "environment"` block already
uses (common fields + provider-specific fields), which is worth
preserving rather than inventing a different convention for input vs.
output. Real, `terraform validate`-checked examples for all three
clouds exist at `examples/hello/environment.yaml` (AWS),
`examples/hello/environment.azure.yaml`, and
`examples/hello/environment.gcp.yaml` — see `examples/README.md` for
how they fit into the overall `init`/`main` flow:

```yaml
# environment.yaml (AWS example)
provider: aws
name: prod
region: eu-west-2
retain_data_on_destroy: true
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
  project_id: my-gcp-project-id   # required; this is what the original
                                    # design silently omitted downstream
```

**Strict/discriminated, not permissive**: only the block matching
`provider:` may be present. An `azure:` block in a file declaring
`provider: aws` is a validation error, not silently ignored — consistent
with this codebase's existing convention that unknown/mismatched
`x-composey` keys are hard errors (`AGENTS.md`, `models/compose.go`'s
`XComposey.UnmarshalJSON`). This is specifically what would have caught
the `--azure-endpoint` dead-flag problem, had this schema existed: an
endpoint override for a provider that isn't the declared target is a
mistake worth failing loudly on, not a flag that quietly does nothing.

### Field mapping from the original CLI flags

Direct lift from `cmd/cloudcompose/init.go`'s original flags (all now
removed — see "Revision 2" below), minus the two confirmed-dead ones
(`--azure-endpoint`, `--gcp-endpoint`). Kept here purely as a historical
map from "what you used to type" to "where that decision now lives,"
for anyone coming from the old flag-based design:

| Common envelope | Old flag | Notes |
|---|---|---|
| `provider` | `--provider`/`-p` | required |
| `name` | `--name`/`-n` | required |
| `region` | `--region`/`-r` | optional; per-provider default preserved (`eu-west-2`/`eastus`/`us-central1`) |
| `tags` | `--tags` | JSON object on the CLI; plain YAML map in the file |
| `retain_data_on_destroy` | `--retain-data` | default `true` |
| `domain` | `--domain` | optional on AWS/Azure (each gets a free CloudFront/Front Door hostname); required for GCP if any service declares `cdn: true` — see "The domain gap" below. Not yet enforced at `init` time, since whether any service uses `cdn: true` isn't known until `cloudcompose main` parses the compose file. |

| `aws:` block | Old flag |
|---|---|
| `vpc_cidr` | `--vpc-cidr` |
| `az_count` | `--az-count` |
| `create_alb` | `--create-alb` |
| `certificate_arn` | `--certificate-arn` |
| `aws_endpoint` | `--aws-endpoint` |

| `azure:` block | Old flag |
|---|---|
| `vnet_cidr` | `--vpc-cidr` (renamed — Azure calls it a VNet, not a VPC; the old flag name was AWS terminology applied uniformly, which this schema is a deliberate chance to fix) |

| `gcp:` block | Old flag |
|---|---|
| `vpc_cidr` | `--vpc-cidr` |
| `project_id` | `--project-id`, **required** — not an `init` input at all before this design; see "The project_id gap" below |

`-o`/`--output` is the one flag that survived both revisions — it's
about where files get *written*, not a decision about the environment
itself, so it doesn't belong in the authored file.

### The `project_id` gap

GCP's generated environment output had no `project_id` field populated
anywhere in `gcp/environment_generator.go`, despite `gcp/infer.go`
depending on `env.ProjectID` throughout. This was found during the
environment-config review (2026-08-08) and is the single most direct
piece of evidence for why an authored, validated input is needed:
`project_id` is unambiguously a *decision* a human makes before running
anything (you know your GCP project before you have any infrastructure),
so it belongs in `environment.yaml`, checked by schema validation at
`init` time — not something discovered missing only when `cloudcompose main`
fails against an incomplete environment much later.

### The `domain` gap

A related category error, found while reviewing this design against
`docker-compose.yml`'s `x-composey.cdn: true` (2026-08-08): `cdn: true`
is a legitimate per-service, per-app decision (does *this* service want
a CDN — the same category as `ingress`/`min_scale`), but on GCP it
cannot be *completed* without a domain the caller owns, since a
Google-managed TLS certificate can't be issued without one (AWS/Azure
each get a free `*.cloudfront.net`/`*.azurefd.net` hostname instead; see
`docs/spikes/gcp/README.md`'s "cdn: true is not self-sufficient on GCP").
A domain is owned once, by the environment/account, not per compose
file — the same reasoning that put `region`/`tags` in the common
envelope rather than duplicating them per provider block.

Added `domain` to `environment.yaml`'s common envelope and to
`GcpEnvironment`, flowing through to the generated `output "environment"`
block. This closes the *schema* gap only: `gcp/infer.go`'s CDN/load-
balancer inference is still the documented no-op it always was (see
`docs/spikes/gcp/README.md`'s "Update (2026-08-08)" annotation) — the
`Domain` field exists so that inference isn't also blocked on a schema
change once it's built, not because it's consumed anywhere yet.

Deliberately not validated at `init` time the way `gcp.project_id` is:
whether any service actually declares `cdn: true` isn't knowable until
`cloudcompose main` parses the compose file, which happens long after
`init` runs. Enforcing "domain is required if any service uses CDN"
belongs in `gcp/infer.go` once that inference exists, not in
`initconfig.Validate`.

### `cloudcompose init` behavior

1. Look for `environment.yaml` in the current directory, or an explicit
   `-f`/`--file` flag (default `environment.yaml`).
2. If found: parse and validate it (required fields present, provider
   block matches `provider:`, no unknown keys), then generate directly
   from it. No flags to merge, no precedence to resolve.
3. If not found: print an error naming the missing path and pointing at
   `examples/hello/environment.yaml`/`docs/authored-environment-config.md`,
   then exit non-zero. There is no flags-only fallback (see "Revision 2"
   below) — creating the first `environment.yaml` for a new environment
   is a file-editing task, the same as creating the first
   `docker-compose.yml` for a new app.
4. On success, in addition to `main.tf.json`, write a copy of the loaded
   `environment.yaml` back out next to it, so the file that produced
   this infrastructure is always sitting alongside it — not just implied
   by shell history. (Since there's no override to resolve anymore, this
   copy is always identical to the input.)

### Multi-environment repos

One environment = one `environment.yaml` = one directory, matching the
`<name>-infrastructure/` output-directory convention. No support for one
file describing multiple named environments (Terraform-workspace-style)
— each environment gets its own directory and its own file, the same way
each already gets its own `main.tf.json` and state.

### Migration

Out of scope. No reverse-generation tooling to produce a starting
`environment.yaml` from an already-deployed environment — this design is
forward-looking for newly-created environments only.

## Revision 1: no fact file

The first version of this design (see git history / earlier revisions of
this doc) generated a fact file — first `environment.yml`, then renamed
to `environment.facts.json` — via a Terraform `local_file` resource, and
`cloudcompose main` read that file. It also added an `environment_config_hash`
field and a drift check comparing the current `environment.yaml`'s hash
against the one recorded in the fact file, warning if they diverged.

Revisited the same day, prompted by the question: given the two-stage
split is the actual goal, does the *output* half need to be a file at
all? It doesn't:

- Terraform already tracks every value the fact file duplicated, in its
  own state, and already exposes it via `terraform output -json` — a
  facility that exists specifically for one root module to hand values
  to something else. The `local_file` resource and the file it wrote
  were pure duplication of a mechanism Terraform already provides.
- Reading live state instead of a cached file removes an entire category
  of bug: there's nothing to go stale, so the hash/drift-check machinery
  built for the file-based version became unnecessary rather than just
  simplified. `environment_config_hash`, `initconfig.Hash`, and
  `warnIfEnvironmentConfigStale` were all removed.
- This was **not** the same as switching to `terraform_remote_state`
  (which was considered and rejected in the same conversation): that
  mechanism resolves values to unresolved HCL expression strings at
  compile time, which would have broken every place Cloud Compose
  Compiler's Go inference branches on an environment field's actual value (e.g.
  `AlbArn != nil` in `aws/permissions.go`). `terraform output -json`
  resolves everything to plain JSON *before* Go ever sees it — the same
  property the file had, just without writing a file to get there.
- The one new requirement this introduces: `cloudcompose main` now needs the
  `terraform` CLI on `PATH`, and the environment directory it's pointed
  at must have real applied state in it. Both were already implicitly
  true before (the fact file itself was only ever produced by `apply`),
  so this makes an existing dependency direct rather than introducing a
  new one.

### What changed as a result

- `cloudcompose main -e <path>` now takes an **environment directory**
  (containing `main.tf.json` + Terraform state), not a file.
- `--azure-endpoint`/`--gcp-endpoint` were already dropped by the first
  revision and stayed dropped.
- No `environment_config_hash` field, no drift check, no
  `environment.facts.json`, no `local_file` resource in any of the three
  `environment_generator.go` files — each now declares only a plain
  `output "environment"` block.
- `internal/compiler/shared/terraform_outputs.go` (`TerraformOutputs`)
  shells out to `terraform output -json`, the same way `cmd/schema-check`
  already shells out to `terraform providers schema -json` — consistent
  precedent already established in this codebase for talking to the
  `terraform` CLI directly rather than adding `hashicorp/terraform-exec`
  as a dependency.

## Revision 2: no flags either

The second version kept the flag-override behavior described in
"`cloudcompose init` behavior" above (an explicitly-passed CLI flag
overrides the file's value for that invocation, via cobra's
`Changed()`). Prompted by the question: given `environment.yaml` exists
specifically to be the reviewable, authored source of truth — the same
role `docker-compose.yml` plays for an app — why should it be any less
"the whole answer" than `docker-compose.yml` is? Nobody expects
`docker compose` itself to take per-field override flags for
`compose.yml`; there's no reason `environment.yaml` should be different,
especially once a real, copyable example existed
(`examples/hello/environment.yaml`) to start from.

Removed all ~14 decision flags (`--provider`, `--name`, `--region`,
`--vpc-cidr`, `--az-count`, `--create-alb`, `--certificate-arn`,
`--aws-endpoint`, `--project-id`, `--domain`, `--retain-data`, `--tags`).
`cloudcompose init` now takes exactly two flags: `-f`/`--file` (which
`environment.yaml` to read) and `-o`/`--output` (where to write the
result) — output location isn't a decision about the environment, so it
stays a flag.

This removed the flag/file merge logic entirely: no more
`cmd.Flags().Changed(...)` checks, no three-way precedence (flag → file
→ hardcoded default) to keep straight, and no way for a flag to silently
diverge from what the committed file says. If `environment.yaml`
doesn't exist, `init` now fails immediately with a message pointing at
`examples/hello/environment.yaml`, rather than falling back to a
flags-only path that no longer exists.

### The one real complication this created: CI needs a unique name per run

`scripts/smoke-test.sh` runs the acceptance
workflows against a shared cloud account, and needs a different resource
name-prefix per run (`ci<run-number>`) to avoid collisions — genuinely
not something a single committed file's `name:` field can express, and
not solvable by adding `name` back as a flag without reintroducing
exactly the override mechanism this revision removed.

Resolved by moving the "many possible names" problem out of
`cloudcompose init` entirely: the script reads a shared, committed
`scripts/ci-environment.{aws,azure}.yaml` (one environment per CI run,
shared across every example that run deploys — those are separate apps
sharing one platform environment, the whole point of the two-stage
split), substitutes `name: PLACEHOLDER` for the run-specific value (and
`region:` too, since that's itself a workflow input for both clouds)
into a generated copy under `build/`, and passes *that* file to
`cloudcompose init -f`. `cloudcompose init` itself never sees a flag or knows
this substitution happened — from its perspective, it just read a file,
exactly like every other invocation.

## What doesn't change

- The overall two-step `init` → `apply` → `main` flow.
- `main` continues to resolve per-app deployment config from
  `docker-compose.yml` + `x-composey` — untouched by any of this.
- Multiple apps (compose files) can point at the same environment
  directory, compiling independently — this was the original motivation
  for the two-stage split and isn't affected by either revision.

## Non-goals

- This does not introduce Cloud Compose Compiler-managed Terraform state
  or an embedded `terraform apply` — `cloudcompose main` reads outputs,
  it doesn't run `plan`/`apply` itself, and doesn't need a backend/state
  config of its own to do so.

## What was built

- `internal/models/init_config.go` — `InitConfig`, `AwsInitConfig`,
  `AzureInitConfig`, `GcpInitConfig`, matching the schema above.
- `internal/compiler/initconfig` — `Load` (reads `environment.yaml`,
  returns `(nil, nil)` if the file doesn't exist — the caller, not this
  package, decides what to do about a missing file) and `Validate` (the
  strict/discriminated checks: name required, provider supported, no
  mismatched provider block, GCP requires `project_id`).
- `cmd/cloudcompose/init.go` — reads `-f`/`--file` (default
  `environment.yaml`); no decision flags. Missing file → a specific
  error message, not a flags-only fallback. Writes a copy of the loaded
  config back to `<output>/environment.yaml` alongside `main.tf.json`.
- `internal/compiler/{aws,azure,gcp}/environment_generator.go` — each
  declares a plain `output "environment"` block only; no `local_file`
  resource, no `hashicorp/local` provider dependency. GCP's generator
  takes a `projectID string` parameter, populated into the output's
  `project_id` — the concrete fix for "The project_id gap" above — and a
  `domain string` parameter, populated into the output's `domain` when
  non-empty — the schema-level fix for "The domain gap" above.
- `internal/models/environment.go` — `GcpEnvironment.Domain *string`,
  read back by `gcp/environment.go`'s loader; not added to
  `Aws/AzureEnvironment`, which don't need it.
- `internal/compiler/shared/terraform_outputs.go` — `TerraformOutputs(dir,
  outputName)` shells out to `terraform output -json` in `dir` and
  returns the named output's value as a `map[string]any`.
- `internal/compiler/shared/terraform_output_decode.go` — `ToStringMap`/
  `ToStringSlice`/`ToStringPtr`, small helpers for decoding that
  `map[string]any` into the typed fields
  `Aws/Azure/GcpEnvironment` declare.
- `internal/compiler/{aws,azure,gcp}/environment.go` — each
  `Load*Environment(dir)` now calls `TerraformOutputs` and decodes the
  result directly, instead of reading/parsing a YAML file.
- `internal/compiler/environment.go` — `LoadEnvironment(dir)` reads the
  `target` field from the live output map (via `TerraformOutputs`)
  instead of probing a file's YAML, then dispatches exactly as before.
- `cmd/cloudcompose/compile.go` / `cmd/cloudcompose/main.go` — the `-e`/`--env`
  flag's help text and internal variable naming (`envDir`, not
  `envFile`) updated to reflect that it's a directory now.
- Removed `internal/compiler/aws/environment_yaml.go`
  (`GenerateEnvironmentYAML`/`pyYAMLScalar`/etc., ~120 lines) — confirmed
  dead code with zero callers outside its own tests before removal.
- `internal/compiler/environment_test.go`'s `TestLoadEnvironment_*` tests
  now set up a real (offline, zero-provider) Terraform directory with an
  `output "environment"` block, run `terraform init`/`apply` in it, and
  call `LoadEnvironment` against that directory — rather than writing a
  YAML file directly, since there's no longer a file-reading code path
  to test.
- `scripts/ci-environment.aws.yaml` / `scripts/ci-environment.azure.yaml`
  — the shared, committed per-cloud environment configs the CI
  acceptance workflows generate a per-run copy of (see "Revision 2"
  above).
- `scripts/smoke-test.sh` — updated to
  pass the environment directory to `-e` (from the earlier revision),
  to generate + use a per-run `environment.yaml` instead of passing
  `--name`/`--region` flags (from this revision), and later unified to
  cover both AWS and Azure via a `PROVIDER` variable (see git history
  for that merge).

