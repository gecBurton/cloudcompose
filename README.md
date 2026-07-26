# Composey: Docker Compose to Terraform Compiler

> [!CAUTION]
> **Project Status: PRE-ALPHA**
> This project is in early development. APIs, models, and generated infrastructure are subject to breaking changes. Not recommended for production use yet.

Composey provides a PaaS-like deployment experience where application engineers deploy an annotated Docker Compose file into an established Environment. The compiler is responsible for inferring AWS infrastructure and generating deterministic Terraform JSON.

**The engineer describes intent; the compiler handles the infrastructure.**

---

## 🚀 Progress & Goals Tracker

### Compiler Pipeline
- [x] **Parse**: Sanitize and load Compose files via `docker compose config`.
- [x] **Normalize**: Transform Compose into a cloud-agnostic semantic model.
- [x] **Infer**: Map semantic intent + environment context to AWS resources.
- [x] **Generate**: Produce deterministic, canonical Terraform JSON.

### Managed Capabilities (v1)
- [x] **Container**: Standard ECS Fargate task deployment.
- [x] **Public HTTP**: ALB ingress routing with per-service path, port and health check. Any number of services may be public.
- [x] **Secrets**: Automatic mapping of Compose `secrets` to AWS Secrets Manager.
- [x] **Storage**: Automatic mapping of *named* `volumes` to AWS S3 Buckets. Bind mounts and anonymous volumes are local-development concerns and are ignored.
- [x] **Managed Object Storage**: Automatically infers AWS S3 from `minio` images.
- [x] **Managed Databases**: Automatically infers AWS RDS (Postgres/MySQL/MariaDB) from library images.
- [x] **Managed Caching**: Automatically infers AWS ElastiCache (Redis) from library images.
- [x] **Managed Queuing**: Smart injection of Redis/ElastiCache endpoints into connection strings.
- [x] **Edge Delivery**: Optional CloudFront + WAF integration for public services.
- [x] **Scheduled Tasks**: Native EventBridge integration for cron-like jobs.
- [x] **Worker**: Support for background services without public ports.

### Quality & Guarantees
- [x] **Determinism**: Byte-identical output for equivalent inputs (canonical JSON/sorting).
- [x] **Isolation**: Full application-level network and identity isolation.
- [x] **Golden Testing**: Regression protection via example snapshots.
- [x] **Terraform Validation**: CI verification using `terraform validate`.
- [x] **Cloud Fidelity**: Integration testing via `LocalStack`.
- [x] **Acceptance Testing**: On-demand runs in a real AWS account, deploying an example end to end and asserting every managed substitution before tearing it down. Verified for `hello` and `doctor` (S3 + RDS + ElastiCache).
- [ ] **Scheduled Acceptance**: Nightly runs rather than on-demand. *(Pending)*

---

## 🛠 Design Principles

1. **Compose is the application DSL.**
2. **Environment is the infrastructure DSL.** (Owned by the Platform Team).
3. **Terraform is a compilation target.**
4. **AWS is an implementation detail.**
5. **The semantic model is cloud-agnostic.**
6. **Deterministic output is a feature.**
7. **Platform defaults are preferred over application configuration.**
8. **Developers describe intent, not infrastructure.**
9. **The compiler is stateless.**
10. **Every compiler stage is independently testable.**

> **Core Philosophy:** Every feature must reduce the amount of AWS knowledge required by an application engineer.

---

## ✨ Managed Capabilities ("PaaS Magic")

Composey doesn't just run containers; it intelligently substitutes cloud-native AWS services for common infrastructure components and supports intent-based scaling.

### 📊 Compute & Scaling
Composey supports the **`x-composey`** extension to allow engineers to specify the relative "size" of their resources and auto-scaling boundaries.

**Supported Sizes:**
- `small` (Default): 256 CPU, 512MB RAM | `db.t3.micro`
- `medium`: 1024 CPU, 2GB RAM | `db.t3.medium`
- `large`: 4096 CPU, 8GB RAM | `db.m5.large`

**Scaling & Resources:**
- `min_scale`: Minimum number of instances (Default: 1).
- `max_scale`: Maximum number of instances (Default: 1). If > 1, creates AWS AppAutoScaling policies for CPU (70%) and Memory (80%).
- `cpu` / `memory`: Explicit Fargate unit overrides.
- `startup_grace_period`: Seconds a newly started instance is given to become healthy before health checks are enforced. (The older ECS-flavoured name `health_check_grace_period` is still accepted.)

**Example:**
```yaml
services:
  web:
    image: my-app
    x-composey:
      size: medium
      min_scale: 2
      max_scale: 10
```

### 🎛 Overriding Inference
Composey infers what a service is from its image, and which service is public from its published port. Neither guess can ever be complete, so both are overridable:

- `capability`: one of `container`, `database`, `cache`, `object-storage`. Use it when a private or vendored image cannot be recognised — or to *stop* an image being substituted.
- `ingress`: how a service is reached from outside — `path`, `port`, `health_path`, optional `priority`. Any number of services may have one, as long as their paths differ. `public: true` is shorthand for `ingress: {}`.

```yaml
services:
  api:
    image: ghcr.io/acme/our-own-postgres-build:1
    x-composey:
      capability: database   # would otherwise run as a container
  web:
    image: our-app
    ports:
      - "8080:8080"
    x-composey:
      ingress:
        path: /
        health_path: /healthz
  api:
    image: our-api
    ports:
      - "8080:8080"
    x-composey:
      ingress:
        path: /api                  # more specific paths are matched first
        health_path: /api/health
```

**Exposure is only ever declared.** Publishing a port does not make a service reachable — composey previously inferred this from whether a published port happened to be 80 or 443, which meant the most consequential property a service has was decided by a coincidence the compose file never stated. A service with no `ingress` is internal: reachable by other services in the application, not from outside. Compiling an application where nothing is exposed prints a warning.

Listener rule priorities are derived from the application name and path, so several applications can share one load balancer listener without colliding, and `priority` can be set explicitly if they ever do.

Recognised images are matched by exact name, including common vendored builds (`pgvector`, `postgis`, `timescaledb`, `bitnami/postgresql`, `redismod`, `valkey`, `keydb`). Anything else needs `capability`.

The whole `x-composey` block is schema-validated: an unknown or misspelled key is an error, not something quietly ignored. An override you can typo is not an override.

### 🔎 Seeing what was inferred
Inference is only safe when a wrong guess is visible. `--explain` reports every decision, what it was made from, and — most usefully — the places where nothing was decided:

```bash
uv run composey --explain -f docker-compose.yml
```

```
backend
  inferred  runs as a container
            image 'placeholder' is not a recognised managed service
  inferred  listens on 8080
            first published port
  warning   nothing wired to db
            no environment variable references 'db'; the service will not be able to find it

db
  inferred  substituted for a managed database
            image 'postgres:17' is a recognised database

application
  warning   NOT reachable from outside
            backend, db publish ports, but none on 80 or 443; set x-composey: public: true
            on the one that should be reachable

7 decision(s), 2 worth checking
```

No environment is needed — every inference reported here is made before the target is consulted — so it is a fast local check on any compose file.

### 🌍 Global Edge & Security (CDN)
For public-facing services, you can enable a global edge presence with built-in security.

- `cdn`: Set to `true` to provision an **AWS CloudFront Distribution** in front of your ALB.
- **Automatic WAF**: Enabling `cdn` automatically attaches an **AWS WAFv2 Web ACL** with the "Core Rule Set" managed rules enabled. AWS only hosts CloudFront-scoped Web ACLs in `us-east-1`, so the compiler emits an aliased `us-east-1` provider for it; the rest of the stack stays in the environment's region.

### ⏰ Scheduled Tasks (Cron)
Turn any container into a serverless scheduled job.

- `schedule`: Either a standard 5-field cron expression (`"0 2 * * *"`) or an interval (`"every 6 hours"`, `"every 30 minutes"`).
- **Behavior**: Services with a `schedule` do not run as persistent ECS services. They are triggered as standalone tasks.

> [!NOTE]
> The schedule is deliberately written in a cloud-neutral form; the compiler renders whatever dialect the target needs (AWS EventBridge wants a six-field cron with a `?` placeholder). AWS's own `cron(...)` and `rate(...)` spellings are still accepted for compatibility, but the neutral form is canonical.

**Example:**
```yaml
services:
  db-cleanup:
    image: my-utils
    command: ["python", "cleanup.py"]
    x-composey:
      schedule: "every 24 hours"
```

### 🗄 Managed Databases (RDS)
If a service uses a standard database image (`postgres`, `mysql`, or `mariadb`), Composey will:
1.  **Substitute Infrastructure**: Provision an **AWS RDS Instance** instead of a container.
2.  **Automate Networking**: Create database subnet groups and security group rules automatically.
3.  **Dynamic Host Injection**: Automatically scan your application's environment variables. If it finds a variable pointing to the database service name (e.g., `DB_HOST: db`), it replaces it with the **actual RDS endpoint address**.

### ⚡️ Managed Caching (ElastiCache)
If a service uses a `redis` image, Composey will:
1.  Provision an **AWS ElastiCache (Redis) Cluster**.
2.  Automatically wire the endpoint into any application containers that depend on it.

### 📦 Managed Object Storage (S3)
If a service uses a `minio` image, Composey will:
1.  **Substitute Infrastructure**: Provision an **AWS S3 Bucket** instead of a container.
2.  **Host Injection**: See [Connection Wiring](#-connection-wiring) below. A variable holding just `blobs` becomes the **bucket ID**; a URL such as `http://blobs:9000` becomes the **bucket domain**.
3.  **Automated Permissions**: Any service that `depends_on` the Minio service is automatically granted full IAM permissions (`s3:*`) to the generated bucket.

### 🔌 Connection Wiring
When a service is substituted for a managed one, every client that referred to it by its Compose name is pointed at the real thing. Resolution is driven by the **values** your Compose file already carries, never by variable names:

| In `compose.yml` | Becomes |
| --- | --- |
| `DB_HOST: db` | the database endpoint |
| `BUCKET_NAME: blobs` | the bucket ID |
| `REDIS_URL: redis://cache:6379` | `redis://<endpoint>:6379` |
| `DATABASE_URL: postgres://u@db:5432/app` | `postgres://u@<endpoint>:5432/app` |
| `S3_ENDPOINT: http://blobs:9000` | `http://<bucket-domain>` |
| anything not naming a service | unchanged |

The scheme, user info and path are preserved; the port comes from the managed service, since the port a container listened on locally rarely survives substitution.

> [!NOTE]
> Composey deliberately preserves the *shape* your Compose file already uses, because that shape is what works locally. If a variable needs a URL, write a URL — `REDIS_URL: redis://cache` — rather than a bare service name.

---

## 📦 Usage

### Requirements
- Python 3.14+
- [uv](https://docs.astral.sh/uv/) (recommended)
- Docker & Docker Compose v2
- Terraform CLI

### Installation (Development)
```bash
# Clone the repository
git clone https://github.com/GBurton/composey.git
cd composey

# Sync dependencies and install the 'composey' command locally
uv sync
```

### Compiling a Project
To compile a Docker Compose file, you must provide an **Environment** configuration (YAML) which describes the target AWS account context (VPC, Cluster, etc.).

> [!TIP]
> You can create a compliant AWS environment automatically using the provided [Terraform infrastructure code](./infra/environment/).

```bash
# Basic compilation
uv run composey --file examples/flask/compose.yml --env examples/prod.yaml

# Short flags with custom project name and output directory
uv run composey -f compose.yml -e prod.yaml -p my-app -o build/terraform

# Show version
uv run composey --version
```

### Environment Configuration (`prod.yaml`)
The `target` field selects the cloud to compile for and determines which of the remaining fields are valid. It currently defaults to `aws`, the only supported target.

```yaml
target: aws
name: prod
vpc_id: vpc-12345678
public_subnets:
  - subnet-abc
  - subnet-def
private_subnets:
  - subnet-ghi
  - subnet-jkl
ecs_cluster_arn: arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster
alb_arn: arn:aws:lb:us-east-1:123456789012:loadbalancer/app/shared-alb/123
alb_listener_arn: arn:aws:lb:us-east-1:123456789012:listener/app/shared-alb/123/456
log_retention_days: 7
```

Log retention is a platform policy rather than an application choice, so it lives here rather than in the compose file.

### Running Tests
The test suite includes unit tests, snapshot comparisons, and local cloud deployment verification via LocalStack.

```bash
make test
```

### Repository Structure
- `composey/compiler/`: The logic for each compilation stage.
- `composey/models/`: Pydantic schemas for Compose, Semantic, AWS, and Environment models.
- `examples/`: End-to-end examples that serve as documentation and "Golden" test snapshots.
- `tests/`: High-fidelity test suite (Unit, Integration, LocalStack).
