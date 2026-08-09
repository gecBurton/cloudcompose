# Examples

Each subdirectory here is a `docker-compose.yml` (plus `x-composey`
annotations where needed) that composey compiles into Terraform. Most
have golden fixtures under `expected/{aws,azure}/main.tf.json` — the
committed, `terraform validate`-checked output composey should produce
for that example, used as regression tests by
`internal/compiler/{aws,azure}/golden_test.go`. GCP has no committed
golden fixtures yet (see `AGENTS.md`'s "GCP has no committed golden
files" note for why).

## The two-step flow: `init` once, `main` many times

Deploying any of these examples for real is a two-step process — bootstrap
a shared environment once, then deploy one or more apps into it:

```bash
cd composey-go

# Step 1: bootstrap the shared platform infrastructure (VPC, ECS
# cluster/Container Apps Environment, etc.) — run once per environment,
# typically by whoever owns the cloud account, not by every developer.
go run ./cmd/composey init -f ../examples/hello/environment.yaml -o /tmp/demo-infrastructure
cd /tmp/demo-infrastructure && terraform init && terraform apply
cd -

# Step 2: deploy an app into that environment — run as often as you like,
# by anyone, without touching the environment itself.
go run ./cmd/composey main -f ../examples/hello/compose.yml -e /tmp/demo-infrastructure
```

`examples/hello/environment.yaml` (and its `environment.azure.yaml`/
`environment.gcp.yaml` siblings) are real, `terraform validate`-checked
authored environment files — the *decisions* `composey init` needs
(region, VPC CIDR, whether to create a load balancer), and `composey
init`'s **only** input; there are no decision flags. They are **not**
the same thing as the `environment.yaml` that ends up sitting next to
`main.tf.json` after `init` actually runs (that one is just a copy of
whichever input file you gave it, written there so the file that
produced a given environment is always visible next to it — not
something to hand-edit) — and neither is the same thing as an
environment's *facts* (its actual VPC ID, ALB ARN, etc. once Terraform
creates them), which are never written to a file at all: `composey main
-e <dir>` reads those live via `terraform output -json` against the
applied environment directory. See `docs/authored-environment-config.md`
for the full design and the reasoning behind that split.

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
| `scaling` | `x-composey.size`/`min_scale`/`max_scale` sizing hints |
| `compute-tuning` | Explicit `cpu:`/`memory:` overrides instead of a named `size:` |
| `platform-config` | Platform-supplied configuration (`x-composey`-inferred, valued outside the compose file) |
| `web-api` | A second, independent public service in the same app |

## Real deployment testing

`scripts/smoke-test.sh`/`scripts/smoke-test-azure.sh` deploy six
different examples (`hello`, `minio-s3`, `build-webapp`, `doctor`,
`web-api`, `production-stack`) against real AWS/Azure as part of this
repo's CI acceptance workflows (see `ci/README.md` for the one-time CI
identity/state-backend setup they depend on). All six share **one**
environment per run, generated from `scripts/ci-environment.aws.yaml`/
`ci-environment.azure.yaml` — not `examples/hello/environment.yaml` —
since a CI run's environment isn't really "for" any one example; it's
one platform environment several separate apps deploy into, the same
multi-app-per-environment pattern the two-step flow above supports.
Each run substitutes a unique `name:` into a generated copy of that
shared file before calling `composey init -f <generated file>` — see
the comments in either smoke-test script for exactly how.

## Running the golden tests yourself

```bash
cd composey-go
go test ./internal/compiler/aws/... -run TestInferAWS_GoldenExamplesByteIdentical -v
go test ./internal/compiler/azure/... -run TestInferAzure_GoldenExamplesByteIdentical -v
```

If you're adding a new example, see `AGENTS.md`'s "Adding a New AWS
Resource"/"Modifying the Semantic Model" sections for what else needs
updating alongside a new `expected/` fixture.
