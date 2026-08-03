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
composey up --provider aws
# or
composey up --provider azure
# or
composey up --provider gcp
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
pip install composey
```

### Deploy to AWS

```bash
# Your existing Docker Compose file
composey up --provider aws
```

That's it. Your app is live at `https://myapp.example.com`.

### Deploy to Azure

```bash
# Same file, different provider
composey up --provider azure
```

### Deploy to GCP

```bash
# Same file, different provider
composey up --provider gcp
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
composey up --provider aws
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
composey explain -f docker-compose.yml
```

```
api
  inferred  runs as serverless container
            image 'myapp' is not a recognized managed service
  inferred  public endpoint
            ports published
  inferred  connects to database
            depends_on references 'db'

db
  inferred  managed database (RDS)
            image 'postgres:15' recognized

Estimated monthly cost: ~$15 (scales to $0 when idle)
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
| **GCP** | 🚧 Planned | Cloud Run | Cloud SQL | Memorystore | GCS |

---

## Installation

### Requirements
- Python 3.14+
- Docker & Docker Compose v2
- Terraform CLI
- Cloud credentials (AWS, Azure, or GCP)

### Install

```bash
pip install composey
```

### Quick Test

```bash
# Clone examples
git clone https://github.com/gecBurton/composey.git
cd composey/examples/hello

# Deploy to AWS
composey up --provider aws

# Or Azure
composey up --provider azure
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

**Current Phase:** Incremental Go migration in progress

- ✅ **Parser & Normalizer:** Go implementation (compose-go library)
- 🚧 **Inference & Generator:** Python (being ported incrementally)

See [plan.md](plan.md) for details on the migration strategy and timeline.

**Installation:** Currently requires Python 3.14+. Single binary distribution coming after migration completes.
