# Cloud Compose Compiler: Docker Compose for the Cloud

> [!CAUTION]
> **Project Status: PRE-ALPHA**
> This project is in early development. APIs, models, and generated infrastructure are subject to breaking changes. Not recommended for production use yet.

**The Mission**: Running services locally via Docker Compose is easy. Running them on the cloud is unnecessarily hard. Cloud Compose Compiler bridges that gap.

**The Experience**:
```bash
# Local development
docker compose up

# Production deployment (same file!)
cloudcompose compile -f docker-compose.yml -e env-prod
```

---

## The Problem

### Local Development (Easy)
```bash
docker compose up
```

That's it. Everything just works:
- Services can reach each other
- Ports are exposed
- Data persists
- No networking configuration
- No load balancer setup
- No IAM policies

### Cloud Deployment (Hard)
```hcl
# 500+ lines of Terraform for the same thing
resource "aws_vpc" "main" { ... }
resource "aws_subnet" "public" { ... }
resource "aws_lb" "main" { ... }
resource "aws_ecs_cluster" "main" { ... }
resource "aws_iam_role" "web" { ... }
# ... and so on
```

You need to understand:
- VPCs, subnets, CIDR blocks
- Load balancers, target groups, listeners
- IAM roles, policies, trust relationships
- Auto-scaling policies
- Security groups and rules

**Why should cloud deployment be 100x more complex than local?**

---

## The Solution

Cloud Compose Compiler takes your Docker Compose file and deploys it to the cloud—optimized for each provider, but with zero additional configuration.

### What You Write

```yaml
services:
  web:
    image: myapp
    ports:
      - "80:8080"
    depends_on:
      - db

  db:
    image: postgres:15
```

### What Cloud Compose Compiler Does

**On AWS**: ECS Fargate + RDS + ALB + Auto-scaling + HTTPS  
**On Azure**: Container Apps + Flexible Server + Built-in ingress  
**On GCP**: Cloud Run + Cloud SQL + Global load balancing

**You never see:**
- VPCs or subnets
- Load balancer configuration
- IAM policies
- Certificate management
- Auto-scaling rules

---

## Core Principles

1. **Docker Compose compatibility** — Valid `docker-compose.yml` just works
2. **Zero config for simple cases** — Same file works locally and in production
3. **Inference over configuration** — Detect postgres → create managed database
4. **Cloud-agnostic by default** — Same file works on AWS, Azure, or GCP
5. **Sensible defaults** — Optimized for cost and performance automatically

---

## Quick Start

### Installation

```bash
git clone https://github.com/gecBurton/cloudcompose.git
cd cloudcompose/cloudcompose-go
go build -o cloudcompose ./cmd/cloudcompose
```

### Set Up an Environment

Each cloud target needs a one-time shared environment (VPC, ALB/Container
Apps Environment, ECS cluster, etc.), created once by a platform team.
`cloudcompose init` takes no decision flags — it reads an authored
`environment.yaml` you write yourself (the same way you'd write
`docker-compose.yml`), not a set of `--flag`s:

```bash
cp examples/hello/environment.yaml ./environment.yaml
# edit name/region/vpc_cidr etc. to taste -- e.g. set name: prod
cloudcompose init
cd env-prod && terraform init && terraform apply
```

`environment.yaml` holds the authored decisions that produce the
infrastructure (region, VPC CIDR, whether to create an ALB — review and
commit this like you would `docker-compose.yml`). `cloudcompose init` writes
a copy of it into the output directory alongside `main.tf.json`. Once
`terraform apply` runs, `cloudcompose compile` reads the resulting facts (VPC
ID, ALB ARN) directly from Terraform's own state via `terraform output
-json` — no separate generated file to keep in sync. See
`docs/authored-environment-config.md` for the full schema, or
`examples/README.md` for a real, runnable walkthrough using
`examples/hello`.

### Deploy to AWS

```bash
cloudcompose compile -f docker-compose.yml -e env-prod
```

That's it. Your app is live behind the shared load balancer / Container App
ingress / Cloud Run URL.

### Deploy to Azure or GCP

```bash
cp examples/hello/environment.azure.yaml ./environment.yaml  # or environment.gcp.yaml for GCP
cloudcompose init
cloudcompose compile -f docker-compose.yml -e env-prod
```

### Check what's running

```bash
cloudcompose ps -f docker-compose.yml -e env-prod
```

Queries the cloud directly for each service's live status — like `docker
compose ps`, but for what's actually running right now, not anything
already implied by `compose.yml` or Terraform state. AWS (ECS service
task counts, ALB target group health) and Azure (Container App revision
replica count and health state) are supported; GCP is not yet. Add
`--json` for a stable, cloud-agnostic JSON array instead of the table
(handy for scripting — see `scripts/smoke-test.sh`'s own use of it).

### Check the logs

```bash
cloudcompose logs -f docker-compose.yml -e env-prod           # every service
cloudcompose logs -f docker-compose.yml -e env-prod web       # just "web"
cloudcompose logs -f docker-compose.yml -e env-prod --since 1h --tail 500
```

Fetches recent log output directly (CloudWatch Logs on AWS, Log
Analytics on Azure), interleaved by timestamp across services — like
`docker compose logs`. Covers container services (app stdout/stderr)
and Postgres databases (RDS/Postgres Flexible Server query and error
logs — MySQL/MariaDB database logs aren't wired up yet). A one-shot
fetch for now, not a continuous `-f`/`--follow` tail. AWS and Azure are
supported; GCP is not yet. Also supports `--json` for the same reason
as `ps` above.

---

## How It Works

### The Magic: Inference

Cloud Compose Compiler reads your Docker Compose file and infers what you need:

| You Write | Cloud Compose Compiler Infers |
|-----------|-----------------|
| `image: postgres` | Managed database (RDS, Cloud SQL, etc.) |
| `image: redis` | Managed cache (ElastiCache, Redis Cache) |
| `image: minio` | Object storage (S3, Blob Storage, GCS) |
| `ports:` | Public HTTPS endpoint with load balancing |
| `depends_on:` | Private service discovery |
| No `ports:` | Internal service only |

### Example: Full-Stack App

```yaml
services:
  web:
    image: myapp/web
    ports:
      - "80:3000"
    depends_on:
      - api

  api:
    image: myapp/api
    depends_on:
      - db
      - cache

  db:
    image: postgres:15

  cache:
    image: redis:7
```

**Deploy to AWS:**
```bash
cloudcompose compile -f docker-compose.yml -e env-prod
```

**What gets created:**
- ECS Fargate services for web and api
- RDS PostgreSQL database
- ElastiCache Redis cluster
- Application Load Balancer with HTTPS
- Auto-scaling policies
- VPC, subnets, security groups
- IAM roles and policies

**You write 20 lines, Cloud Compose Compiler generates 500+ lines of optimized Terraform.**

---

## Features

### 🔒 HTTPS Automatically

Every public service gets HTTPS with automatic certificate management.

### 🗄 Managed Services

Standard images are automatically upgraded to managed services:
- `postgres` → Managed database
- `redis` → Managed cache
- `minio` → Object storage

### 📈 Autoscaling

Set how many instances a service should keep running, and scale on CPU,
memory, or request count:

```yaml
services:
  api:
    image: myapp
    x-cloud:
      min_scale: 2   # Always at least 2 instances
      max_scale: 10
      auto_scaling:
        metrics:
          - type: cpu
            target_value: 70
```

Cloud Compose Compiler translates the same declaration into ECS target-tracking (AWS),
KEDA scale rules (Azure), or Cloud Run autoscaling (GCP) — whichever is
idiomatic for the target cloud.

### 🔍 See What Was Inferred

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

## Configuration (When You Need It)

### Simple is Default

Most apps need zero additional configuration. Just `docker-compose.yml`.

### Progressive Enhancement

Add hints only when you need them:

```yaml
services:
  api:
    image: myapp
    x-cloud:
      size: large  # More resources
      min_scale: 2  # Always keep 2 instances warm
```

Unknown keys under `x-cloud` are a hard error rather than silently
ignored, so a typo in one of these hints fails at compile time, not at
runtime.

---

## Supported Clouds

| Cloud | Status | Compute | Database | Cache | Storage | Scheduled tasks | CDN |
|-------|--------|---------|----------|-------|---------|------------------|-----|
| **AWS** | ✅ Verified against real deployments | ECS Fargate | RDS | ElastiCache | S3 | ✅ EventBridge | ✅ CloudFront + WAF |
| **Azure** | ✅ Verified against real deployments (see [`docs/azure-todo.md`](docs/azure-todo.md)) | Container Apps | Flexible Server | Cache for Redis | Blob Storage | ✅ Container Apps Jobs | ✅ Front Door (no WAF) |
| **GCP** | ⚠️ Compiles and passes structural tests; not yet verified against a real deployment or covered by golden-file regression tests | Cloud Run | Cloud SQL | Memorystore | Cloud Storage | ❌ not implemented | ❌ not implemented |

GCP support is intentionally less mature than AWS/Azure — see
`AGENTS.md`'s "GCP has no committed golden
files" note for the testing gap specifically.

Azure has closed most of its feature/security gaps with AWS (RBAC and
Key Vault-backed secrets, compose `secrets:`/platform `config:` support,
database sizing, autoscaling) — see
[`docs/azure-aws-parity-todo.md`](docs/azure-aws-parity-todo.md) for the
full, actively-maintained tracker of what's done and what's still open
(a WAF equivalent and a couple of smaller items remain).

---

## Installation

### Requirements
- Go 1.26+ (to build)
- Docker (for services with a `build:` section)
- Terraform CLI
- Cloud credentials (AWS, Azure, or GCP)

### Install

Download a prebuilt binary from the
[Releases page](https://github.com/gecBurton/cloudcompose/releases) —
archives are published for Linux, macOS, and Windows (amd64 and arm64).
For example, on macOS (Apple Silicon):

```bash
curl -LO https://github.com/gecBurton/cloudcompose/releases/latest/download/cloudcompose_<version>_darwin_arm64.tar.gz
tar -xzf cloudcompose_<version>_darwin_arm64.tar.gz
chmod +x cloudcompose
```

Or build from source:

```bash
git clone https://github.com/gecBurton/cloudcompose.git
cd cloudcompose/cloudcompose-go
go build -o cloudcompose ./cmd/cloudcompose
```

### Quick Test

No cloud account needed — see what any example compiles to with
`--demo`:

```bash
# From the cloudcompose-go directory
./cloudcompose compile -f ../examples/hello/compose.yml -d aws
```

Swap `-d aws` for `-d azure`/`-d gcp` to see the same compose file
compiled for a different cloud. The output is real, valid Terraform
JSON, but uses placeholder resource IDs — not deployable as-is.

To actually deploy:

```bash
# From the cloudcompose-go directory
./cloudcompose init -f ../examples/hello/environment.yaml
(cd env-demo && terraform init && terraform apply)

./cloudcompose compile -f ../examples/hello/compose.yml -e env-demo
```

---

## Documentation

- [Azure deployment status](docs/azure-todo.md)
- [Azure/AWS feature parity gap analysis](docs/azure-aws-parity-todo.md)
- [Design docs and spikes](docs/)
- [Examples](examples/)

---

## Philosophy

> **Docker Compose simplicity + Cloud scale**

We believe deploying to the cloud should be as easy as running locally. No networking expertise required. No infrastructure boilerplate. Just your services, running at scale.

---

## Contributing

See `AGENTS.md` for architecture, package layout, and development
workflow. There is no `CONTRIBUTING.md` yet.

---

## License

No license file has been added to this repository yet.

---

## Project Status

**Current Phase:** Pre-alpha. All parsing, normalization, inference, generation, and CLI logic run in `cloudcompose-go`.

**Installation:** Build the `cloudcompose` binary with Go 1.26+ (see Installation above). Prebuilt cross-platform binaries and a package-manager install path are not yet available.
