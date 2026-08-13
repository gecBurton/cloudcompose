# Intent-Based Abstractions for Multi-Cloud

The docker-compose.yml captures **application intent**. The compiler translates intent to cloud-specific implementation. Engineers write the same compose file regardless of target cloud.

## Guiding Principle

**Compose file declares:** "I need this to handle 1000 concurrent users"
**Compiler decides:** Target tracking (AWS) vs KEDA HTTP scaling (Azure) vs autoscaling (GCP)

**Compose file declares:** "These services should talk to each other"
**Compiler decides:** Security groups (AWS) vs VNet integration (Azure) vs VPC connectors (GCP)

## Intent-Based Abstractions

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

## Benefits

1. **Same compose file** works on any cloud
2. **Intent is clear** — what the app needs, not how to get it
3. **Cloud optimizes** — each backend uses that cloud's idiomatic mechanism
4. **Easy migration** — change target, same source

## Trade-offs

1. **Less control** — can't specify cloud-specific optimizations directly from the compose file
2. **Different behavior** — scaling may behave differently in practice (target tracking vs KEDA vs Cloud Run autoscaling), even given the same declared intent
3. **Feature gaps** — some features don't map cleanly to every cloud (see `AGENTS.md`'s "Ported-not-fixed bugs exist deliberately" note for cases where a divergence is intentional rather than a bug)
