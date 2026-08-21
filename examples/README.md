# Examples

Each subdirectory here is a `docker-compose.yml` (plus `x-cloud`
annotations where needed) that cloudcompose compiles into Terraform. Most
have golden fixtures under `expected/{aws,azure}/main.tf.json` — the
committed, `terraform validate`-checked output cloudcompose should produce
for that example, used as regression tests by
`internal/compiler/{aws,azure}/golden_test.go`. GCP has no committed
golden fixtures yet (see `AGENTS.md`'s "GCP has no committed golden
files" note for why).

## The fastest path: `--demo`, no setup at all

To see what any example compiles to on a given cloud, with no cloud
account, no credentials, and no `cloud-compose env init` step:

```bash
cd cloudcompose-go
go run ./cmd/cloudcompose compile -f ../examples/hello/compose.yml -d aws
```

`-d aws`/`-d azure`/`-d gcp` swaps in a built-in synthetic environment
(plausible-looking placeholder resource IDs) instead of reading a real
one. The output is genuinely valid Terraform JSON, but not deployable
as-is — it's for evaluation only. See
`docs/authored-environment-config.md`'s "Evaluating without a live
environment" section for the full design.

## The fast path to a real deployment: `env up` + `compose up`

For the common case -- one app, one environment -- `cloud-compose env up`
runs the environment's `init` -> `apply` flow described below in one
command, and `cloud-compose compose up` does the same for the app's own
`compile` -> `apply` flow, each stopping to show you its own `terraform
apply`'s plan and prompt for confirmation exactly as it would if you ran
the steps by hand (no `-auto-approve` anywhere). `--env` means the
authored environment.yaml file on `env up`/`env init`, and the
already-applied environment *directory* on `compose up`/`compile`/`ps`/
`logs`/`compose down`/`env down` (see the step-by-step flow's own note on
this below).

```bash
cd cloudcompose-go
go run ./cmd/cloudcompose env up --env ../examples/hello/environment.yaml
go run ./cmd/cloudcompose compose up -f ../examples/hello/compose.yml --env ../examples/hello/env-demo
```

If you're deploying more than one app into the same environment, or want
to see each generated Terraform manifest before running `terraform
apply` at all, use the step-by-step flow below instead -- `env up`
always re-runs the environment's own `apply` (Terraform reports "No
changes" if it's already up to date), which is fine the first time but
means running it again before deploying a second app re-prompts you on
the shared environment's plan too, even though nothing changed.

## The step-by-step flow: `env init` once, `compile` many times

Deploying any of these examples for real is a two-step process — bootstrap
a shared environment once, then deploy one or more apps into it:

```bash
cd cloudcompose-go

# Step 1: bootstrap the shared platform infrastructure -- VPC + ECS
# cluster on AWS; resource group + Log Analytics workspace + VNet on
# Azure (no Container Apps Environment: that's per-app now, created by
# `compile` below, not shared -- see docs/azure-app-isolation-design.md
# for why Azure's isolation boundary doesn't work the same way AWS's
# does). Run once per environment, typically by whoever owns the cloud
# account, not by every developer.
#
# Neither env init nor compile take an output-location flag: env init
# always writes to <dir of -e>/env-<name> (here, next to
# examples/hello/environment.yaml, so env-demo/ lands in
# ../examples/hello/, since that file's `name:` is `demo`).
go run ./cmd/cloudcompose env init -e ../examples/hello/environment.yaml
cd ../examples/hello/env-demo && terraform init && terraform apply
cd -

# Step 2: deploy an app into that environment -- run as often as you
# like, by anyone, for as many apps as you want to share this
# environment (each compile call reads the same, already-applied
# environment facts independently -- this is the main practical reason
# to share one environment across apps: fewer NAT Gateways/ALBs paid
# for, rather than one set per app). On AWS this only adds the app's own
# resources inside the shared cluster/VPC; on Azure this also creates
# the app's own Container Apps Environment and delegated subnets (real,
# per-app infrastructure, not just app-level resources) -- pass
# --subnet-index to give it a distinct, non-overlapping slice of the
# environment's reserved address space if more than one app shares this
# environment.
#
# -e must be the applied environment directory -- the one `env init`
# wrote main.tf.json into and you just ran `terraform apply` in above,
# not environment.yaml itself (that's what `env init`'s/`env up`'s own
# --env means instead -- see this file's "fast path" section above).
# compile's own output lands at
# <dir of -f>/app-<environment name>-<project name> (here, app-demo-hello/,
# "hello" being -f's own containing directory name, compile's default
# --project) -- named after both the environment and the project so the
# same compose.yml can be compiled again against a different
# environment.yaml/env-<name> (e.g. dev vs prod), or under a different
# --project against the same environment, without overwriting this
# output.
go run ./cmd/cloudcompose compile -f ../examples/hello/compose.yml -e ../examples/hello/env-demo
```

`examples/hello/environment.yaml` (and its `environment.azure.yaml`/
`environment.gcp.yaml` siblings) are real, `terraform validate`-checked
authored environment files — the *decisions* `cloud-compose env init`
needs (region, VPC CIDR, whether to create a load balancer), and
`cloud-compose env init`'s **only** input; there are no decision flags.
They are **not** the same thing as the `environment.yaml` that ends up
sitting next to `main.tf.json` after `env init` actually runs (that one
is just a copy of whichever input file you gave it, written there so
the file that produced a given environment is always visible next to
it — not something to hand-edit) — and neither is the same thing as an
environment's *facts* (its actual VPC ID, ALB ARN, etc. once Terraform
creates them), which are never written to a file at all: `cloud-compose
compile -e <dir>` reads those live via `terraform output -json` against
the applied environment directory. See
`docs/authored-environment-config.md` for the full design and the
reasoning behind that split.

To try a different example, or a different cloud, swap `hello`/`aws` for
any other example directory and the sibling `environment.<cloud>.yaml`
(or write your own — see the schema in
`docs/authored-environment-config.md`).

## What each example is for

| Example | Demonstrates |
|---|---|
| `hello` | The minimum path: one public container, no managed services |
| `flask`, `flask-redis`, `flask-s3` | A database/cache/object-storage relationship each |
| `nginx-flask-mysql` | MySQL/MariaDB instead of PostgreSQL |
| `minio-s3` | An object-storage-only relationship, no database |
| `doctor` | Building an image from a `Dockerfile` rather than using a pre-built one |
| `build-webapp` | Image build + push, combined with a database |
| `production-stack` | The most feature-dense example: CDN, WAF, autoscaling, scheduled task, multiple relationships |
| `scaling` | `x-cloud.size`/`min_scale`/`max_scale` sizing hints |
| `compute-tuning` | Explicit `cpu:`/`memory:` overrides instead of a named `size:` |
| `platform-config` | Platform-supplied configuration (`x-cloud`-inferred, valued outside the compose file) |
| `web-api` | A second, independent public service in the same app |

## Real deployment testing

`scripts/smoke-test.sh` deploys six
different examples (`hello`, `minio-s3`, `build-webapp`, `doctor`,
`web-api`, `production-stack`) against real AWS/Azure as part of this
repo's CI acceptance workflows (`PROVIDER=aws` or `PROVIDER=azure`; see
`ci/README.md` for the one-time CI
identity/state-backend setup they depend on). Each run deploys exactly
one of the six into its own fresh environment (`NAME=ci<run-number>`,
generated from `scripts/ci-environment.aws.yaml`/
`ci-environment.azure.yaml` — not `examples/hello/environment.yaml`,
since a CI run's environment isn't really "for" any one example) —
not six examples sharing one environment simultaneously; the
multi-app-per-environment pattern the two-step flow above supports is
never actually exercised by CI today, since only one app ever deploys
per run (on Azure, this also means `--subnet-index` is never passed —
see `scripts/smoke-test.sh`'s own comment at that call site for why
defaulting to `0` is correct here).
Each run substitutes a unique `name:` into a generated copy of that
shared file before calling `cloud-compose env init -e <generated file>` — see
the comments in the smoke-test script for exactly how.

## Running the golden tests yourself

```bash
cd cloudcompose-go
go test ./internal/compiler/aws/... -run TestInferAWS_GoldenExamplesByteIdentical -v
go test ./internal/compiler/azure/... -run TestInferAzure_GoldenExamplesByteIdentical -v
```

If you're adding a new example, see `AGENTS.md`'s "Adding a New AWS
Resource"/"Modifying the Semantic Model" sections for what else needs
updating alongside a new `expected/` fixture.

## Sharing one environment across multiple users

Everything above assumes a single machine applying `cloud-compose env init`/
`env up` against its own local Terraform state. Once more than one person
(or a laptop and CI) needs to apply against the *same* environment,
state has to live somewhere shared, with locking -- see
`docs/authored-environment-config.md`'s "Sharing one environment across
multiple users" section and `docs/multi-user-state.md` for the full
design. [`bootstrap-state/`](bootstrap-state/) has the one-time,
manually-applied Terraform each cloud's `backend:` block needs to
already exist before `environment.yaml` references it.
