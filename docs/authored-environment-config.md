# Authored Environment Configuration

`environment.yaml` is the authored, reviewable input for
`cloudcompose init` — the environment-config equivalent of
`docker-compose.yml`. Two things are cleanly separated:

| | What | Lifecycle |
|---|---|---|
| **Input** | `environment.yaml` | Authored, committed, reviewed |
| **Output** | Terraform's own state, read live via `terraform output -json` | Never a file Cloud Compose Compiler writes; always current by construction |

`cloudcompose init` reads `environment.yaml` as its **only** input — no
decision flags. To change a decision, edit the file and re-run `init`.

`cloudcompose main -e <environment-directory>` reads the environment's
facts by running `terraform output -json` in that directory (which must
already have `terraform apply` run in it) and decoding its `environment`
output.

## Evaluating without a live environment: `--demo`

`cloudcompose main -d <cloud>` (`aws`/`azure`/`gcp`) generates the same
Terraform JSON a real compile would, using a built-in synthetic
environment with plausible-looking placeholder resource IDs instead of
reading a real one — for a prospective user to see what their compose
file becomes on a given cloud without first running `cloudcompose init`
or holding any cloud credentials at all.

`-e` and `-d` are mutually exclusive and one is required: there is no
default when neither is given, the same "one way to configure, not two"
reasoning `init`'s own flag set follows. The output is genuinely valid
Terraform JSON (every demo environment is checked against the real
provider schema via `terraform validate`), but it is not deployable
as-is — the placeholder IDs (`vpc-demo...`, fake ARNs, etc.) don't
correspond to anything real. `cloudcompose main` prints a stderr banner
saying so whenever `-d` is used.

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
(`AGENTS.md`, `models/compose.go`'s `XCloud.UnmarshalJSON`).

Real, `terraform validate`-checked examples for all three clouds exist
at `examples/hello/environment.yaml` (AWS),
`examples/hello/environment.azure.yaml`, and
`examples/hello/environment.gcp.yaml`.

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
| `domain` | optional on AWS/Azure (each gets a free CloudFront/Front Door hostname); required for GCP if any service declares `cdn: true` (Google-managed cert requires domain ownership) — not enforced at `init` time, since whether `cdn: true` is used isn't known until `cloudcompose main` parses the compose file |

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

`-o`/`--output` is the one flag `init` still takes (where files get
written — not a decision about the environment itself).

## Known gap: GCP CDN inference

`domain` exists on `GcpEnvironment` and flows through to the generated
output, but `gcp/infer.go`'s CDN/load-balancer inference is still a
documented no-op — the field exists so inference isn't blocked on a
schema change once it's built, not because anything consumes it yet.

## Non-goals

- No Cloud Compose Compiler-managed Terraform state or embedded
  `terraform apply` — `cloudcompose main` reads outputs, it doesn't run
  `plan`/`apply` itself.
- No multi-environment-per-file support (Terraform-workspace-style) —
  one `environment.yaml` = one directory = one environment.
- No reverse-generation tooling to produce a starting `environment.yaml`
  from an already-deployed environment.

## Implementation reference

- `internal/models/init_config.go` — `InitConfig`,
  `Aws/Azure/GcpInitConfig`.
- `internal/compiler/initconfig` — `Load` (reads `environment.yaml`,
  returns `(nil, nil)` if missing) and `Validate` (strict/discriminated
  checks).
- `cmd/cloudcompose/init.go` — `-f`/`--file` (default
  `environment.yaml`), `-o`/`--output`; no decision flags.
- `internal/compiler/{aws,azure,gcp}/environment_generator.go` — each
  declares a plain `output "environment"` block; no `local_file`
  resource.
- `internal/compiler/shared/terraform_outputs.go` — `TerraformOutputs(dir,
  outputName)` shells out to `terraform output -json`.
- `internal/compiler/shared/terraform_output_decode.go` — decode helpers
  (`ToStringMap`/`ToStringSlice`/`ToStringPtr`).
- `internal/compiler/{aws,azure,gcp}/environment.go` —
  `Load*Environment(dir)` calls `TerraformOutputs` and decodes directly.
- CI note: `scripts/ci-environment.{aws,azure}.yaml` are shared,
  committed environment configs; `scripts/smoke-test.sh` substitutes a
  per-run `name:`/`region:` into a generated copy under `build/` before
  passing it to `cloudcompose init -f`, since a single committed file
  can't express a unique name per CI run.
