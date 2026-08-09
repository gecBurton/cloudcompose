# Composey: Docker Compose for the Cloud

> [!CAUTION]
> **Project Status: PRE-ALPHA**
> This project is in early development. APIs, models, and generated infrastructure are subject to breaking changes. Not recommended for production use yet.

**The Mission**: Running services locally via Docker Compose is easy. Running them on the cloud is unnecessarily hard. Composey bridges that gap.

**The Experience**:
```bash
# Local development
docker compose up

# Production deployment (same file!)
composey main -f docker-compose.yml -e prod-infrastructure
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

Composey takes your Docker Compose file and deploys it to the cloud—optimized for each provider, but with zero additional configuration.

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

### What Composey Does

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
git clone https://github.com/gecBurton/composey.git
cd composey/composey-go
go build -o composey ./cmd/composey
```

### Set Up an Environment

Each cloud target needs a one-time shared environment (VPC, ALB/Container
Apps Environment, ECS cluster, etc.), created once by a platform team:

```bash
composey init --provider aws --name prod
cd prod-infrastructure && terraform init && terraform apply
```

This writes `environment.yaml` — the authored decisions that produced
the infrastructure (region, VPC CIDR, whether to create an ALB — review
and commit this like you would `docker-compose.yml`) — alongside
`main.tf.json`. Once `terraform apply` runs, `composey main` reads the
resulting facts (VPC ID, ALB ARN) directly from Terraform's own state via
`terraform output -json` — no separate generated file to keep in sync.
Re-running `composey init -f prod-infrastructure/environment.yaml` picks
up that file as input; any flag passed explicitly overrides its value
for that run. See `docs/authored-environment-config.md` for the full
design.

### Deploy to AWS

```bash
composey main -f docker-compose.yml -e prod-infrastructure
```

That's it. Your app is live behind the shared load balancer / Container App
ingress / Cloud Run URL.

### Deploy to Azure or GCP

```bash
composey init --provider azure --name prod
composey main -f docker-compose.yml -e prod-infrastructure

composey init --provider gcp --name prod --project-id my-gcp-project-id
composey main -f docker-compose.yml -e prod-infrastructure
```

---

## How It Works

### The Magic: Inference

Composey reads your Docker Compose file and infers what you need:

| You Write | Composey Infers |
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
composey main -f docker-compose.yml -e prod-infrastructure
```

**What gets created:**
- ECS Fargate services for web and api
- RDS PostgreSQL database
- ElastiCache Redis cluster
- Application Load Balancer with HTTPS
- Auto-scaling policies
- VPC, subnets, security groups
- IAM roles and policies

**You write 20 lines, Composey generates 500+ lines of optimized Terraform.**

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
    x-composey:
      min_scale: 2   # Always at least 2 instances
      max_scale: 10
      auto_scaling:
        metrics:
          - type: cpu
            target_value: 70
```

Composey translates the same declaration into ECS target-tracking (AWS),
KEDA scale rules (Azure), or Cloud Run autoscaling (GCP) — whichever is
idiomatic for the target cloud.

### 🔍 See What Was Inferred

```bash
composey main -f docker-compose.yml --explain
```

```
api
  inferred  runs as a container
            image 'myapp' is not a recognised managed service
  declared  served at / on port 80
            declared by x-composey: ingress
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
    x-composey:
      size: large  # More resources
      min_scale: 2  # Always keep 2 instances warm
```

Unknown keys under `x-composey` are a hard error rather than silently
ignored, so a typo in one of these hints fails at compile time, not at
runtime.

---

## Supported Clouds

| Cloud | Status | Compute | Database | Cache | Storage | Scheduled tasks | CDN |
|-------|--------|---------|----------|-------|---------|------------------|-----|
| **AWS** | ✅ Verified against real deployments | ECS Fargate | RDS | ElastiCache | S3 | ✅ EventBridge | ✅ CloudFront + WAF |
| **Azure** | ✅ Verified against real deployments (see [`docs/azure-todo.md`](docs/azure-todo.md)) | Container Apps | Flexible Server | Cache for Redis | Blob Storage | ✅ Container Apps Jobs | ✅ Front Door (no WAF) |
| **GCP** | ⚠️ Compiles and passes structural tests; not yet verified against a real deployment or covered by golden-file regression tests | Cloud Run | Cloud SQL | Memorystore | Cloud Storage | ❌ not implemented | ❌ not implemented |

GCP support is intentionally less mature than AWS/Azure — see `plan.md`
for why, and `AGENTS.md`'s "GCP has no committed golden
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

```bash
git clone https://github.com/gecBurton/composey.git
cd composey/composey-go
go build -o composey ./cmd/composey
```

### Quick Test

```bash
# From the composey-go directory
./composey init --provider aws --name demo
(cd demo-infrastructure && terraform init && terraform apply)

./composey main -f ../examples/hello/compose.yml -e demo-infrastructure
```

---

## Documentation

- [Design history and migration log](plan.md)
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

**Current Phase:** Fully migrated to Go. All parsing, normalization, inference, generation, and CLI logic run in `composey-go` — there is no Python runtime dependency anymore.

See [plan.md](plan.md) for the migration history.

**Installation:** Build the `composey-go` binary with Go 1.26+ (see Installation above). Prebuilt cross-platform binaries and a package-manager install path are not yet available.
