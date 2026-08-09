# Design: Authored Environment Configuration

> **Status: implemented (2026-08-08, revised same day).** Implemented
> twice in one day: the first version generated a fact file
> (`environment.facts.json`) via a Terraform `local_file` resource and
> included a hash-based drift check against it. That file was removed
> the same day in favor of reading Terraform's own live state directly
> (`terraform output -json`) — see "Revision: no fact file" below for
> why, and "What was built" for the current, final mapping from design
> to code. The authored-input half of this doc (`environment.yaml`, the
> schema, `composey init`'s behavior) is unchanged by that revision.
>
> The two-step `init`/`main` command split this doc assumes throughout
> (bootstrap an environment once, deploy apps against it many times) is
> unchanged from how `composey init`/`composey main` actually work today
> — this doc only revises what `init` takes as *input*, not the overall
> command shape.

## The problem

`docker-compose.yml` is source: authored by a human, versioned, reviewed,
and deterministic to compile. The environment side of composey had no
equivalent. Before this design, "configuring an environment" meant
passing CLI flags to `composey init` once, which were never recorded
anywhere:

```bash
composey init --provider aws --name prod --region eu-west-2 \
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

- `composey init --provider gcp` generated an environment file missing
  `project_id` — a field GCP inference depends on heavily — because
  nothing validated the generated file's completeness before handing it
  to `composey main`. Silent until deploy time.
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
| **Output** | Terraform's own state, read live via `terraform output -json` | Never a file composey writes; always current, by construction |

`composey init` reads `environment.yaml` as its primary input. CLI flags
remain, but as *overrides* on top of the file — mirroring how Compose
itself treats `docker-compose.yml` plus environment-variable overrides:
the file is the source of truth; a flag explicitly passed on the command
line wins over the file for that one invocation, but doesn't get written
back into it.

`composey main -e <environment-directory>` reads the environment's facts
by running `terraform output -json` in that directory (which must be a
directory `composey init` generated, with `terraform apply` already run
in it) and decoding its `environment` output. See "Revision: no fact
file" below for why this replaced a generated file.

### Schema: common envelope + discriminated provider block

Mirrors the shape the Terraform `output "environment"` block already
uses (common fields + provider-specific fields), which is worth
preserving rather than inventing a different convention for input vs.
output:

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

### Field mapping from CLI flags

Direct lift from `cmd/composey/init.go`'s flags, minus the two
confirmed-dead ones (`--azure-endpoint`, `--gcp-endpoint`):

| Common envelope | Flag | Notes |
|---|---|---|
| `provider` | `--provider`/`-p` | required |
| `name` | `--name`/`-n` | required |
| `region` | `--region`/`-r` | optional; per-provider default preserved (`eu-west-2`/`eastus`/`us-central1`) |
| `tags` | `--tags` | JSON object on the CLI; plain YAML map in the file |
| `retain_data_on_destroy` | `--retain-data` | default `true` |
| `domain` | `--domain` | optional on AWS/Azure (each gets a free CloudFront/Front Door hostname); required for GCP if any service declares `cdn: true` — see "The domain gap" below. Not yet enforced at `init` time, since whether any service uses `cdn: true` isn't known until `composey main` parses the compose file. |

| `aws:` block | Flag |
|---|---|
| `vpc_cidr` | `--vpc-cidr` |
| `az_count` | `--az-count` |
| `create_alb` | `--create-alb` |
| `certificate_arn` | `--certificate-arn` |
| `aws_endpoint` | `--aws-endpoint` |

| `azure:` block | Flag |
|---|---|
| `vnet_cidr` | `--vpc-cidr` (renamed — Azure calls it a VNet, not a VPC; the old flag name was AWS terminology applied uniformly, which this schema is a deliberate chance to fix) |

| `gcp:` block | Flag |
|---|---|
| `vpc_cidr` | `--vpc-cidr` |
| `project_id` | `--project-id`, **required** — not an `init` input at all before this design; see "The project_id gap" below |

`--output`/`-o` stays a CLI-only concept (it's about where files get
*written*, not a decision about the environment itself, so it doesn't
belong in the authored file).

### The `project_id` gap

GCP's generated environment output had no `project_id` field populated
anywhere in `gcp/environment_generator.go`, despite `gcp/infer.go`
depending on `env.ProjectID` throughout. This was found during the
environment-config review (2026-08-08) and is the single most direct
piece of evidence for why an authored, validated input is needed:
`project_id` is unambiguously a *decision* a human makes before running
anything (you know your GCP project before you have any infrastructure),
so it belongs in `environment.yaml`, checked by schema validation at
`init` time — not something discovered missing only when `composey main`
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
`composey main` parses the compose file, which happens long after
`init` runs. Enforcing "domain is required if any service uses CDN"
belongs in `gcp/infer.go` once that inference exists, not in
`initconfig.Validate`.

### `composey init` behavior

1. Look for `environment.yaml` in the current directory (or an explicit
   `-f`/`--file` flag, mirroring `composey main -f`'s own convention).
2. If found: parse and validate it (required fields present, provider
   block matches `provider:`, no unknown keys). Any CLI flag explicitly
   passed (`cmd.Flags().Changed(...)`, the same mechanism already used
   for `--region`'s per-provider default logic) overrides the
   corresponding file value for this invocation only.
3. If not found: fall back to flags-only, exactly as before this design
   — not a breaking change. A first-time `composey init --provider aws
   --name prod` with no file still works, quick-start ergonomics
   preserved.
4. On success, in addition to `main.tf.json`, write the resolved
   `environment.yaml` back out (or confirm the existing one is
   unchanged) so the file that produced this infrastructure is always
   sitting next to it — not just implied by shell history.

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

## Revision: no fact file

The first version of this design (see git history / earlier revisions of
this doc) generated a fact file — first `environment.yml`, then renamed
to `environment.facts.json` — via a Terraform `local_file` resource, and
`composey main` read that file. It also added an `environment_config_hash`
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
  compile time, which would have broken every place composey's Go
  inference branches on an environment field's actual value (e.g.
  `AlbArn != nil` in `aws/permissions.go`). `terraform output -json`
  resolves everything to plain JSON *before* Go ever sees it — the same
  property the file had, just without writing a file to get there.
- The one new requirement this introduces: `composey main` now needs the
  `terraform` CLI on `PATH`, and the environment directory it's pointed
  at must have real applied state in it. Both were already implicitly
  true before (the fact file itself was only ever produced by `apply`),
  so this makes an existing dependency direct rather than introducing a
  new one.

### What changed as a result

- `composey main -e <path>` now takes an **environment directory**
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

## What doesn't change

- The overall two-step `init` → `apply` → `main` flow.
- `main` continues to resolve per-app deployment config from
  `docker-compose.yml` + `x-composey` — untouched by any of this.
- Multiple apps (compose files) can point at the same environment
  directory, compiling independently — this was the original motivation
  for the two-stage split and isn't affected by the fact-file revision.

## Non-goals

- This does not introduce composey-managed Terraform state or an
  embedded `terraform apply` — `composey main` reads outputs, it doesn't
  run `plan`/`apply` itself, and doesn't need a backend/state config of
  its own to do so.

## What was built

- `internal/models/init_config.go` — `InitConfig`, `AwsInitConfig`,
  `AzureInitConfig`, `GcpInitConfig`, matching the schema above.
- `internal/compiler/initconfig` — `Load` (reads `environment.yaml`,
  returns `(nil, nil)` if the file doesn't exist) and `Validate` (the
  strict/discriminated checks: name required, provider supported, no
  mismatched provider block, GCP requires `project_id`).
- `cmd/composey/init.go` — reads `-f`/`--file` (default
  `environment.yaml`) if present, applies any explicitly-passed flag as
  an override, and writes the resolved config back to
  `<output>/environment.yaml` alongside `main.tf.json`. No
  `--azure-endpoint`/`--gcp-endpoint`; `--project-id` required for GCP.
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
- `cmd/composey/compile.go` / `cmd/composey/main.go` — the `-e`/`--env`
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
- `scripts/smoke-test.sh` / `scripts/smoke-test-azure.sh` updated to pass
  the environment directory to `-e`, not a file inside it — the flow was
  already `init` → `apply` → `main`, so this only changed what `-e`
  itself points at, not the ordering.
