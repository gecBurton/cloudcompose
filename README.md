# cloud-compose up!

> [!CAUTION]
> **Project Status: PRE-ALPHA**, APIs, models, and generated infrastructure are subject to breaking changes. Not recommended for production use yet.

Running services locally with Docker Compose is easy. Deploying the same app to the cloud usually means hand-writing hundreds of lines of Terraform, VPCs, load balancers, IAM policies, auto-scaling rules. `cloud-compose` reads your existing `docker-compose.yml` and a new file, `environment.yaml`, that describes your cloud environment, and compiles them straight to deployable Terraform for AWS, Azure, or GCP.

```bash
# Local development
docker compose up

# Production deployment (same file!)
cloud-compose up -f docker-compose.yml -e environment.yaml
```

No `--flags` describing your infrastructure, no new config format to learn, it infers what it can (`image: postgres` → a managed database) and lets you override the rest with a small `x-cloud:` block when you need to. `-f`/`--file` is optional — every command auto-discovers `compose.yaml`/`compose.yml`/`docker-compose.yaml`/`docker-compose.yml` in the current directory if you don't pass it, the same way `docker compose` itself does.

---

## Install

Download a prebuilt binary from the
[Releases page](https://github.com/gecBurton/cloudcompose/releases),
archives are published for Linux, macOS, and Windows (amd64 and arm64):

```bash
curl -LO https://github.com/gecBurton/cloudcompose/releases/download/v0.3.0/cloud-compose_0.3.0_darwin_arm64.tar.gz
tar -xzf cloud-compose_0.3.0_darwin_arm64.tar.gz
chmod +x cloud-compose
```

Or build from source (requires Go 1.26+):

```bash
git clone https://github.com/gecBurton/cloudcompose.git
cd cloudcompose
make build
```

You'll also need the **Terraform CLI**, **Docker** (only if a service has a `build:` section), and credentials for whichever cloud you're deploying to.

---

## Set up an environment

The `environment.yaml` describes the essential features of your environment such as:
* which cloud you are using
* what region it is in
* where the terraform state is stored

The environment also contains all the most expensive parts of the deployment — VPC, ALB/Container Apps Environment, ECS cluster, etc — and as such many applications can be deployed to the same environment, saving money and deployment time.

The environment must be set up before any application can be deployed to it:

```bash
cloud-compose env up --env environment.yaml
```

A typical environment might be:

```yaml
provider: aws
name: preproduction
region: eu-west-2
retain_data_on_destroy: true
high_availability_enabled: false # Multi-AZ roughly doubles RDS compute cost; opt-in
backup_retention_days: 7
log_retention_days: 7
tags:
  Team: platform

aws:
  vpc_cidr: 10.0.0.0/16
  az_count: 2
  create_alb: true

backend:
  local:
    path: ./terraform-aws.tfstate
```

`backend:` must declare either `local:` (state stays on this machine, as shown above) or `remote:` (a real remote backend, with locking, for sharing one environment across multiple people/CI). See `docs/authored-environment-config.md` for the remote-backend shapes.

---

## How it works

`--explain` reports every inference the compiler makes and writes nothing — no cloud account or `environment.yaml` needed:

```bash
cloud-compose compile -f docker-compose.yml --explain
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

`--env`/`-e` always means the authored `environment.yaml`, on every command (`env init`, `env up`, `compile`, `up`, `down`, `ps`, `logs`, `env down`). It resolves the environment from `environment.yaml` alone — it never creates or modifies the environment itself; if it hasn't been applied yet (`env init`/`env up` never ran), `up`/`compile` fail clearly rather than applying it on your behalf. Environment changes are always a deliberate act, never a side effect of deploying an app. See `docs/deployment-identity-design.md` for the full reasoning.

Once the environment is applied, deploy an app into it:

```bash
cloud-compose up --env environment.yaml
```

`up` runs `compile` → `terraform apply` on the app. Every `apply` still shows its plan and prompts for confirmation, exactly as if you'd run the steps by hand. That's it, your app is live behind the shared load balancer / Container App ingress / Cloud Run URL.

If you'd rather review each stage yourself instead of `env up`'s one-step apply:

```bash
cloud-compose env init --env environment.yaml
cd env-<name> && terraform init && terraform apply && cd ..
cloud-compose compile --env environment.yaml
```

(`env init` derives `env-<name>` from `environment.yaml`'s own `name:` field, alongside `environment.yaml` itself, and writes a copy of the resolved config there too.) Deploying to Azure or GCP instead just means starting from `environment.azure.yaml`/`environment.gcp.yaml`.

See `docs/authored-environment-config.md` for the full `environment.yaml` schema, or `examples/README.md` for a real, runnable walkthrough.

---

## Inspection

Having deployed your application in a similar manner to docker-compose you can now inspect it using
the same familiar tooling:

```bash
# Live status of each service -- ECS/ALB on AWS, Container Apps on Azure
cloud-compose ps --env environment.yaml

# Recent logs, one service or every service, interleaved by timestamp
cloud-compose logs --env environment.yaml
cloud-compose logs --env environment.yaml web --since 1h --tail 500

# Tear the app down again (never touches the shared environment)
cloud-compose down --env environment.yaml

# Tear the shared environment down too, once no app depends on it
cloud-compose env down --env environment.yaml
```

`ps`/`logs` query the cloud directly, not anything already implied by `compose.yml` or Terraform state, AWS and Azure are supported; GCP is not yet. Both take `--json` for scripting. Every command that runs Terraform (`env up`, `env down`, `up`, `down`) stays interactive by default; pass `--auto-approve` for non-interactive callers like CI.

---

## What it infers

| You write | `cloud-compose` infers |
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

On Azure, an app also needs a top-level (not per-service) `x-cloud.azure.subnet_index` — each app gets its own Container Apps Environment for isolation, carved out of the shared environment's address space, so this picks which slice:

```yaml
name: myapp
x-cloud:
  azure:
    subnet_index: 0   # unique per app sharing one environment; required on Azure, ignored elsewhere
services:
  api:
    image: myapp
```

See `docs/azure-app-isolation-design.md` for why.

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
- [Deployment identity: how environments and apps are located](docs/deployment-identity-design.md)
- [Azure per-app isolation and subnet allocation](docs/azure-app-isolation-design.md)
- [Multi-user state: remote backends and safe teardown](docs/multi-user-state.md)
- [Azure deployment status](docs/azure-todo.md)
- [Azure/AWS feature parity gap analysis](docs/azure-aws-parity-todo.md)
- [More design docs and spikes](docs/)
- [Examples](examples/)

## Contributing

See `AGENTS.md` for architecture, package layout, and development workflow. There is no `CONTRIBUTING.md` yet, and no license file has been added to this repository yet.
