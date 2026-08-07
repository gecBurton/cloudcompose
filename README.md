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
composey main -f docker-compose.yml -e prod-env.yaml
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

This writes `environment.yml`, which every app deployment references.

### Deploy to AWS

```bash
composey main -f docker-compose.yml -e prod-infrastructure/environment.yml
```

That's it. Your app is live behind the shared load balancer / Container App
ingress / Cloud Run URL.

### Deploy to Azure or GCP

```bash
composey init --provider azure --name prod
composey main -f docker-compose.yml -e prod-infrastructure/environment.yml

composey init --provider gcp --name prod
composey main -f docker-compose.yml -e prod-infrastructure/environment.yml
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
composey main -f docker-compose.yml -e prod-infrastructure/environment.yml
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

### 🚀 Serverless by Default

Services scale to zero when not in use. You pay only for what you use.

```yaml
services:
  api:
    image: myapp
    # Implicit: scale to zero, auto-scale up
```

### 🔒 HTTPS Automatically

Every public service gets HTTPS with automatic certificate management.

### 🗄 Managed Services

Standard images are automatically upgraded to managed services:
- `postgres` → Managed database
- `redis` → Managed cache
- `minio` → Object storage

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

### Escape Hatches

For special cases, cloud-specific overrides are available:

```yaml
services:
  websocket:
    image: myapp
    x-composey:
      type: persistent  # Don't scale to zero
```

---

## Supported Clouds

| Cloud | Status | Compute | Database | Cache | Storage |
|-------|--------|---------|----------|-------|---------|
| **AWS** | ✅ Ready | ECS Fargate | RDS | ElastiCache | S3 |
| **Azure** | ✅ Ready | Container Apps | Flexible Server | Cache for Redis | Blob Storage |
| **GCP** | ✅ Ready | Cloud Run | Cloud SQL | Memorystore | Cloud Storage |

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

./composey main -f ../examples/hello/compose.yml -e demo-infrastructure/environment.yml
```

---

## Documentation

- [Getting Started](docs/getting-started.md)
- [Configuration Reference](docs/configuration.md)
- [Cloud Providers](docs/providers.md)
- [Examples](examples/)
- [Architecture](docs/architecture.md)

---

## Philosophy

> **Docker Compose simplicity + Cloud scale**

We believe deploying to the cloud should be as easy as running locally. No networking expertise required. No infrastructure boilerplate. Just your services, running at scale.

---

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md).

---

## License

MIT License - see [LICENSE](LICENSE)

---

## Project Status

**Current Phase:** Fully migrated to Go. All parsing, normalization, inference, generation, and CLI logic run in `composey-go` — there is no Python runtime dependency anymore.

See [plan.md](plan.md) for the migration history.

**Installation:** Build the `composey-go` binary with Go 1.26+ (see Installation above). Prebuilt cross-platform binaries and a package-manager install path are not yet available.
