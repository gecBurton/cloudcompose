# Compiler Design: Intent-Based Abstractions and Cloud Coverage

## Intent-Based Abstractions for Multi-Cloud

The docker-compose.yml captures **application intent**. The compiler translates intent to cloud-specific implementation. Engineers write the same compose file regardless of target cloud.

### Guiding Principle

**Compose file declares:** "I need this to handle 1000 concurrent users"
**Compiler decides:** Target tracking (AWS) vs KEDA HTTP scaling (Azure) vs autoscaling (GCP)

**Compose file declares:** "These services should talk to each other"
**Compiler decides:** Security groups (AWS) vs VNet integration (Azure) vs VPC connectors (GCP)

### 1. Scaling Intent

One `x-cloud.auto_scaling.metrics` list, cloud-agnostic in the compose
file, translated per cloud at compile time:

```yaml
services:
  api:
    x-cloud:
      min_scale: 2
      max_scale: 10
      auto_scaling:
        metrics:
          - type: cpu
            target_value: 70
          - type: requests_per_target
            target_value: 1000
```

**Compiler translates** (`internal/compiler/{aws,azure,gcp}/compute.go`):
- AWS: ECS target-tracking autoscaling policies (`handleAutoscaling` in `aws/compute.go`)
- Azure: KEDA HTTP/CPU/memory scale rules on the Container App (`inferContainerApps` in `azure/compute.go`)
- GCP: Cloud Run's own autoscaling annotations (`inferCloudRunServicesGcp` in `gcp/infer.go`)

If `auto_scaling` is omitted, `min_scale`/`max_scale` alone still drive a
sensible default per cloud — the metrics block is an escalation, not a
requirement.

### 2. Communication Intent

```yaml
services:
  api:
    networks:
      - backend
    # Intent: "I need to receive traffic from the internet" (declared via `ingress:`)

  worker:
    networks:
      - backend
    # Intent: "I only talk to other services, not the internet" (no ingress)

  database:
    # No networks = isolated
    # Intent: "I only accept connections from specific services"
```

**Compiler translates**:
- AWS: security groups, ALB listener rules, CloudMap service discovery
- Azure: VNet integration, built-in Container Apps Environment service discovery
- GCP: VPC connectors, IAM invoker bindings between Cloud Run services

### 3. Identity Intent

Cloud Compose Compiler infers what a service needs to access from its declared
connections and capability — there's no explicit `x-cloud.access` block
in the compose file; the compiler works this out from `depends_on` plus
each dependency's `capability`.

**Compiler translates**:
- AWS: IAM roles with policies scoped to the specific resources inferred (`internal/compiler/aws/permissions.go`)
- Azure: managed identities with role assignments (`internal/compiler/azure/managed.go`)
- GCP: service accounts with IAM bindings

### 4. Secret Intent

Standard Compose `secrets:`, not a cloud-specific reference:

```yaml
services:
  api:
    secrets:
      - db_password  # Intent: "I need the database password"

secrets:
  db_password:
    external: true  # "Platform provides this"
```

**Compiler translates**:
- AWS: Secrets Manager references + IAM permission to read them
- Azure: Key Vault references + managed identity
- GCP: Secret Manager references + service account binding

### Benefits

1. **Same compose file** works on any cloud
2. **Intent is clear** — what the app needs, not how to get it
3. **Cloud optimizes** — each backend uses that cloud's idiomatic mechanism
4. **Easy migration** — change target, same source

### Trade-offs

1. **Less control** — can't specify cloud-specific optimizations directly from the compose file
2. **Different behavior** — scaling may behave differently in practice (target tracking vs KEDA vs Cloud Run autoscaling), even given the same declared intent
3. **Feature gaps** — some features don't map cleanly to every cloud (see `AGENTS.md`'s "Ported-not-fixed bugs exist deliberately" note for cases where a divergence is intentional rather than a bug)

## GCP: known gaps

Azure reached full feature parity with AWS (RBAC/Key Vault-backed
secrets, compose `secrets:`/platform `config:` support, database
sizing, autoscaling, per-app network isolation, WAF-equivalent rate
limiting on Front Door). GCP is intentionally the least-verified of the
three clouds — it has never been tested against a real deployment (see
`AGENTS.md`) — and has the gaps below as an accepted scope decision,
not oversights that crept in unnoticed.

### Open items

- **Network-isolation model unreviewed** — Cloud Run's VPC connector
  scoping, ingress settings, and per-service IAM haven't been checked
  against the same lens as Azure's per-app isolation work (see
  `docs/deployment-model.md`'s "Per-app isolation on Azure" section).
- **No size-ceiling rejection** — Azure rejects Consumption-tier
  cpu/memory combinations above its ceiling; GCP has no equivalent
  check. (GCP's own size-to-resources table has already been fixed to
  derive from `shared.SizeMappings`, the same table AWS/Azure use — only
  the ceiling-rejection logic itself is missing.)
- **Backup/HA settings not wired** — Cloud SQL has its own equivalent
  settings; left for a follow-up.
- **`log_retention_days` placement unreconciled** — currently a
  common-envelope field applied to AWS and Azure only; an early GCP
  spike recommended keeping it AWS-only. Not decided either way.
- **CDN/domain inference is a documented no-op** (`gcp/infer.go`) and
  **Cloud SQL's connection-string handling** (unix-socket path vs.
  host:port) is unconfirmed — see `docs/spikes/gcp/README.md`.

### Not a gap (intentional differences, any cloud)

- Azure's shared-server-per-engine database topology vs AWS's
  dedicated-instance-per-service — a real capability tradeoff (shared
  engines can't get independent HA/sizing/maintenance windows), not an
  oversight. See `azure/managed.go`'s `largestServiceSize`.
- Azure's one-shared-ACR-per-environment vs AWS's
  one-ECR-repo-per-service — both valid.
- No `managed_rule` WAF support on Azure Front Door — Standard SKU
  (this codebase's default) only supports `custom_rule`; the
  AWS-equivalent managed rule sets need the Premium SKU, which would
  change cost for every existing deployment. Not planned unless a
  Premium-tier option is explicitly requested.
- Rate-schedule rejection when a value doesn't divide evenly into cron —
  a real expressiveness gap vs AWS's native `rate(...)`, handled as a
  clear compile-time error per this project's "reject what you can't
  express" principle.
- Retries/timeouts unmodeled for scheduled jobs — absent on all three
  clouds equally.

See `docs/spikes/gcp/README.md` for the rest of GCP's design-time
findings, and `docs/spikes/azure/README.md` for the equivalent
historical spike for Azure.
