# cloud compose up
Docker Compose for the Cloud

> [!CAUTION]
> **Project Status: PRE-ALPHA**, APIs, models, and generated infrastructure are subject to breaking changes. Not recommended for production use yet.

Running services locally with Docker Compose is easy. Deploying the same app to the cloud usually means hand-writing hundreds of lines of Terraform, VPCs, load balancers, IAM policies, auto-scaling rules. Cloud Compose Compiler reads your existing `docker-compose.yml` and compiles it straight to deployable Terraform for AWS, Azure, or GCP:

```bash
# Local development
docker compose up

# Production deployment (same file!)
cloudcompose compile -f docker-compose.yml -e env-prod
```

No `--flags` describing your infrastructure, no new config format to learn, it infers what it can (`image: postgres` → a managed database) and lets you override the rest with a small `x-cloud:` block when you need to.

---

## Install

Download a prebuilt binary from the
[Releases page](https://github.com/gecBurton/cloudcompose/releases),
archives are published for Linux, macOS, and Windows (amd64 and arm64):

```bash
curl -LO https://github.com/gecBurton/cloudcompose/releases/latest/download/cloudcompose_<version>_darwin_arm64.tar.gz
tar -xzf cloudcompose_<version>_darwin_arm64.tar.gz
chmod +x cloudcompose
```

Or build from source (requires Go 1.26+):

```bash
git clone https://github.com/gecBurton/cloudcompose.git
cd cloudcompose/cloudcompose-go
go build -o cloudcompose ./cmd/cloudcompose
```

You'll also need the **Terraform CLI**, **Docker** (only if a service has a `build:` section), and credentials for whichever cloud you're deploying to.

### Try it with no cloud account

`--demo` compiles any example against placeholder resource IDs, real, valid Terraform JSON, just not deployable as-is:

```bash
# From the cloudcompose-go directory
./cloudcompose compile -f ../examples/hello/compose.yml -d aws   # or -d azure / -d gcp
```

See exactly what it inferred and why, before compiling anything for real:

```bash
cloudcompose compile -f docker-compose.yml --explain
```
```
api
  inferred  runs as a container
            image 'myapp' is not a recognised managed service
  declared  served at / on port 80
            declared by x-cloud: ingress
  inferred  may connect to db
            depends_on

db
  inferred  substituted for a managed database
            image 'postgres:15' is a recognised database

7 decision(s)
```

---

## Deploy for real

Every cloud target needs a one-time shared environment (VPC, ALB/Container Apps Environment, ECS cluster, etc.), created once, then reused by every app deployed into it. You author it the same way you'd author `docker-compose.yml`: a small, reviewable `environment.yaml`, not a pile of `--flags`.

```bash
cp examples/hello/environment.yaml ./environment.yaml
# edit name/region/vpc_cidr etc. to taste -- e.g. set name: prod
```

You'll also need a `docker-compose.yml` for the app itself. Every command below auto-discovers `compose.yaml`/`compose.yml`/`docker-compose.yaml`/`docker-compose.yml` in the current directory if you don't pass `-f` explicitly, the same way `docker compose` itself does.

From here, pick one:

### Fast path: one command

`cloudcompose up` runs `init` → `terraform apply` → `compile` → `terraform apply` in one go. Every `apply` still shows its plan and prompts for confirmation, exactly as if you'd run the four steps by hand:

```bash
cloudcompose up --env environment.yaml
```

That's it, your app is live behind the shared load balancer / Container App ingress / Cloud Run URL.

### Two-step path: review each stage

Use this if you're deploying more than one app into the same environment, or want to see the generated Terraform before anything applies.

```bash
cloudcompose init
cd env-prod && terraform init && terraform apply && cd ..
cloudcompose compile -e env-prod
```

`cloudcompose init` writes a copy of `environment.yaml` alongside the generated `main.tf.json`. Once `terraform apply` runs, `cloudcompose compile` reads the resulting facts (VPC ID, ALB ARN, …) directly from Terraform's own state, no separate generated file to keep in sync. Deploying to Azure or GCP instead just means starting from `environment.azure.yaml`/`environment.gcp.yaml`.

See `docs/authored-environment-config.md` for the full `environment.yaml` schema, or `examples/README.md` for a real, runnable walkthrough.

---

## Operate it like `docker compose`

```bash
# Live status of each service -- ECS/ALB on AWS, Container Apps on Azure
cloudcompose ps -e env-prod

# Recent logs, one service or every service, interleaved by timestamp
cloudcompose logs -e env-prod
cloudcompose logs -e env-prod web --since 1h --tail 500

# Tear the app down again (never touches the shared environment)
cloudcompose down -e env-prod
```

`ps`/`logs` query the cloud directly, not anything already implied by `compose.yml` or Terraform state, AWS and Azure are supported; GCP is not yet. Both take `--json` for scripting. Every command that runs Terraform (`up`, `down`) stays interactive by default; pass `--auto-approve` for non-interactive callers like CI.

---

## What it infers

| You write | Cloud Compose Compiler infers |
|-----------|-----------------|
| `image: postgres` | A managed database (RDS, Cloud SQL, Flexible Server) |
| `image: redis` | A managed cache (ElastiCache, Memorystore, Cache for Redis) |
| `image: minio` | Object storage (S3, GCS, Blob Storage) |
| `ports:` | A public HTTPS endpoint with load balancing and a certificate |
| `depends_on:` | Private service discovery between containers |
| No `ports:` | An internal-only service |

Most apps need nothing beyond this. When you do need to override a decision, instance size, autoscaling, a specific database engine, add a small `x-cloud:` hint:

```yaml
services:
  api:
    image: myapp
    x-cloud:
      size: large       # more CPU/memory
      min_scale: 2      # always keep 2 instances warm
      max_scale: 10
      auto_scaling:
        metrics:
          - type: cpu
            target_value: 70
```

The same declaration becomes ECS target-tracking on AWS, KEDA scale rules on Azure, or Cloud Run autoscaling on GCP, whichever is idiomatic for that cloud. Unknown keys under `x-cloud` are a hard compile-time error rather than silently ignored, so a typo fails immediately instead of surfacing later at deploy time.

---

## Supported clouds

| Cloud | Status | Compute | Database | Cache | Storage | Scheduled tasks | CDN |
|-------|--------|---------|----------|-------|---------|------------------|-----|
| **AWS** | ✅ Verified against real deployments | ECS Fargate | RDS | ElastiCache | S3 | ✅ EventBridge | ✅ CloudFront + WAF |
| **Azure** | ✅ Verified against real deployments (see [`docs/azure-todo.md`](docs/azure-todo.md)) | Container Apps | Flexible Server | Cache for Redis | Blob Storage | ✅ Container Apps Jobs | ✅ Front Door (no WAF) |
| **GCP** | ⚠️ Compiles and passes structural tests; not yet verified against a real deployment or covered by golden-file regression tests | Cloud Run | Cloud SQL | Memorystore | Cloud Storage | ❌ not implemented | ❌ not implemented |

GCP is intentionally less mature than AWS/Azure, see `AGENTS.md`'s "GCP has no committed golden files" note for the testing gap specifically. Azure has closed most of its feature/security gaps with AWS (RBAC and Key Vault-backed secrets, compose `secrets:`/platform `config:` support, database sizing, autoscaling), see [`docs/azure-aws-parity-todo.md`](docs/azure-aws-parity-todo.md) for what's still open.

---

## Documentation

- [Authored environment.yaml schema](docs/authored-environment-config.md)
- [Azure deployment status](docs/azure-todo.md)
- [Azure/AWS feature parity gap analysis](docs/azure-aws-parity-todo.md)
- [More design docs and spikes](docs/)
- [Examples](examples/)

## Contributing

See `AGENTS.md` for architecture, package layout, and development workflow. There is no `CONTRIBUTING.md` yet, and no license file has been added to this repository yet.
