# Azure Port Design Analysis

> **Historical design doc, written before Azure support existed at all**
> (pre-dates the Python→Go migration described in `plan.md`). Kept for the
> "why Container Apps over AKS, why KEDA/managed-identity mattered" design
> reasoning, which turned out to be substantially correct — but read the
> note below before treating anything here as current.
>
> **What actually happened**: "Option 1: Minimal Azure MVP" (Container
> Apps + Flexible Server + system-assigned managed identity) is what
> shipped, and it's since been verified against real Azure deployments
> (see `docs/azure-todo.md`). KEDA-style HTTP/queue scaling, Key Vault, and
> Container Apps Jobs (scheduled tasks) were all eventually added too —
> see `plan.md`'s Phase 4 for the actual Azure port history.
>
> **What did NOT happen**: the cloud-agnostic `Identity`/`SecretRef`/
> `ScalingRule` unified semantic model this doc proposes in "Semantic
> Model Gaps" and "Revised Semantic Model" (Option 2) was **not** adopted.
> AWS and Azure inference logic stayed separate, in their own
> `internal/compiler/{aws,azure}` packages, each with its own
> cloud-specific config on the resource structs rather than one shared
> abstract type. If you're looking for how identity/secrets/scaling
> actually work today, read `internal/compiler/azure/managed.go` and
> `internal/models/azure.go` directly rather than this doc's proposed
> models.

This document analyzes what an Azure port would look like and identifies gaps in our current abstraction.

## Why Azure is a Good Test

Azure is fundamentally different from AWS in ways that will stress-test our abstractions:

1. **Higher-level managed services** - Less control, more opinionated
2. **Different networking model** - VNet integration vs security groups
3. **Different scaling model** - KEDA event-driven scaling vs target tracking
4. **Different secret management** - Key Vault references vs Secrets Manager

## Service Mapping: AWS → Azure

### Compute: ECS Fargate → Container Apps

| Feature | AWS ECS Fargate | Azure Container Apps |
|---------|----------------|---------------------|
| **Unit of deployment** | Task Definition + Service | Container App |
| **Scaling** | Target tracking (CPU/memory/requests) | KEDA event-driven (HTTP requests, queue depth, schedule) |
| **Networking** | VPC, security groups, ALB | VNet integration, no ALB needed (built-in ingress) |
| **Service discovery** | CloudMap (DNS) | Built-in (automatic) |
| **Secrets** | Secrets Manager + IAM | Key Vault + managed identity |
| **Logs** | CloudWatch Logs | Log Analytics Workspace |
| **Minimum scale** | 1 (or 0 with EventBridge) | 0-30 (scale to zero supported) |

**Key differences:**
- Azure Container Apps has built-in ingress - no separate load balancer
- Scaling is event-driven (KEDA), not target tracking
- Can scale to zero (saves money)
- No need for service discovery - it's automatic

### Database: RDS → Azure Database for PostgreSQL/MySQL

| Feature | AWS RDS | Azure Database for PostgreSQL |
|---------|---------|------------------------------|
| **Deployment model** | Instance-based | Server (compute) + storage (decoupled) |
| **High availability** | Multi-AZ | Zone redundant (high availability) |
| **Read replicas** | Native, cross-region | Read replicas (same region only) |
| **Serverless** | Aurora Serverless v2 | Flexible Server (auto-scaling compute) |
| **Connection** | Direct connection | Connection proxy (pgbouncer built-in) |
| **Networking** | VPC, security groups | VNet integration, private endpoints |

**Key differences:**
- Azure Flexible Server auto-scales compute (not just storage)
- Built-in connection pooling (pgbouncer)
- No cross-region read replicas

### Cache: ElastiCache → Azure Cache for Redis

| Feature | AWS ElastiCache | Azure Cache for Redis |
|---------|-----------------|----------------------|
| **Tiers** | Cluster, Standard | Basic, Standard, Premium, Enterprise |
| **Clustering** | Native (Redis Cluster) | Premium tier only |
| **Persistence** | AOF, RDB | RDB, AOF |
| **VNet** | Subnet groups | VNet injection (Premium+) |

**Key differences:**
- Azure Enterprise tier is different (Redis Enterprise from Redis Labs)
- VNet injection requires Premium tier

### Object Storage: S3 → Azure Blob Storage

| Feature | AWS S3 | Azure Blob Storage |
|---------|--------|-------------------|
| **Access tiers** | Standard, IA, Glacier | Hot, Cool, Archive |
| **Static website** | Native support | Static website hosting |
| **CDN integration** | CloudFront | Azure CDN / Front Door |

**Relatively straightforward mapping**

## Azure-Specific Challenges

### 1. Container Apps Environment

Azure requires a **Container Apps Environment** - a boundary around related apps:

```yaml
# Azure-specific environment configuration
container_apps_environment:
  name: "prod-web-api"
  vnet_integration:
    vnet_id: "/subscriptions/.../vnet-prod"
    infrastructure_subnet_id: "/subscriptions/.../subnet-infra"
```

This maps to our `network_isolation_segments` but is more rigid.

### 2. KEDA Scaling vs Target Tracking

Our current auto-scaling model assumes target tracking:

```python
class AutoScalingMetric(BaseModel):
    type: Literal["cpu", "memory", "requests_per_target"]
    target_value: float  # Target percentage/value
```

KEDA uses **event sources** and **scaling rules**:

```yaml
# Azure/KEDA scaling
scaling:
  rules:
    - name: http-rule
      custom:
        type: http
        metadata:
          concurrentRequests: "50"
    - name: queue-rule
      custom:
        type: azure-servicebus
        metadata:
          queueName: my-queue
          messageCount: "100"
```

**Gap:** Our semantic model doesn't support event-driven scaling rules.

### 3. Built-in Ingress vs ALB

AWS: Separate ALB resource with listener rules
Azure: Built-in ingress on Container Apps

```python
# Current semantic model
class Ingress(BaseModel):
    path: str  # Used for ALB listener rules
    port: Optional[int]
    health_check: HealthCheck
```

Azure doesn't need `path` for routing (it uses the FQDN), but `path` is still relevant for:
- Health check path
- Rewriting rules
- Exposing multiple services on same Container Apps Environment

### 4. Secrets: Key Vault References

AWS: Secrets Manager + IAM role to read
Azure: Key Vault + Managed Identity

```python
# AWS approach
secrets:
  - name: DB_PASSWORD
    valueFrom: "arn:aws:secretsmanager:..."

# Azure approach
secrets:
  - name: DB_PASSWORD
    keyVaultUrl: "https://myvault.vault.azure.net/secrets/db-password"
    identity: "/subscriptions/.../identities/app-identity"
```

**Gap:** Our secret model assumes AWS-style references.

### 5. Managed Identities vs IAM Roles

AWS: IAM roles attached to tasks
Azure: Managed identities (System-assigned or User-assigned)

```python
# AWS
iam_role: str  # Role ARN

# Azure
identity:
  type: SystemAssigned | UserAssigned
  user_assigned_identities: ["/subscriptions/.../id1", "/subscriptions/.../id2"]
```

**Gap:** Our permission model assumes IAM roles.

## Semantic Model Gaps

### Gap 1: Event-Driven Scaling

**Current:**
```python
class AutoScalingConfig(BaseModel):
    metrics: list[AutoScalingMetric]  # CPU, memory, requests
```

**Needed for Azure:**
```python
class ScalingRule(BaseModel):
    name: str
    type: Literal["http", "queue", "schedule", "cpu", "memory"]
    metadata: dict[str, str]  # Event-specific config

class AutoScalingConfig(BaseModel):
    # For AWS-style target tracking
    metrics: Optional[list[AutoScalingMetric]]
    # For Azure/KEDA event-driven
    rules: Optional[list[ScalingRule]]
```

### Gap 2: Identity Model

**Current:** Assumes IAM roles (implicit in AWS backend)

**Needed:**
```python
class Identity(BaseModel):
    type: Literal["iam_role", "managed_identity"]
    # AWS
    role_arn: Optional[str]
    # Azure
    managed_identity_ids: Optional[list[str]]
```

### Gap 3: Secret Provider

**Current:** Assumes Secrets Manager

**Needed:**
```python
class SecretReference(BaseModel):
    name: str
    provider: Literal["secrets_manager", "key_vault"]
    # AWS
    secret_arn: Optional[str]
    # Azure
    key_vault_url: Optional[str]
    identity: Optional[str]  # Managed identity to use
```

## Azure Environment Configuration

```python
class AzureEnvironment(BaseEnvironment):
    target: Literal["azure"] = "azure"
    region: str = "eastus"
    
    # Container Apps Environment
    container_apps_environment:
        name: str
        vnet_integration:
            vnet_id: str
            infrastructure_subnet_id: str
    
    # Log Analytics (required for Container Apps)
    log_analytics_workspace_id: str
    
    # Database
    postgresql_flexible_server:
        resource_group: str
        # ... or use existing
    
    # Identity
    managed_identity: Optional[str]  # User-assigned identity for apps
```

## Revised Semantic Model

To support both AWS and Azure, we'd need:

```python
# Auto-scaling: Support both models
class AutoScalingConfig(BaseModel):
    # AWS target tracking (current)
    target_tracking: Optional[list[AutoScalingMetric]]
    # Azure/KEDA event-driven
    event_driven: Optional[list[ScalingRule]]
    # Common
    min_replicas: int = 0  # Azure can scale to 0
    max_replicas: int
    cooldown: Optional[int]

# Identity: Cloud-agnostic
class ServiceIdentity(BaseModel):
    type: Literal["aws_iam_role", "azure_system_assigned", "azure_user_assigned"]
    # Provider-specific config as dict
    config: dict[str, Any]

# Secrets: Cloud-agnostic reference
class SecretRef(BaseModel):
    name: str
    source: Literal["compose_secret", "aws_secrets_manager", "azure_key_vault"]
    # Provider-specific reference
    ref: str
    # For Azure: which identity to use
    identity: Optional[str]
```

## Recommendation

### Option 1: Minimal Azure Support (MVP)

Focus on Container Apps with simple scaling (HTTP requests only):

- Single Container Apps Environment per app
- HTTP-based scaling only (ignores queues for now)
- System-assigned managed identities
- Flexible Server for PostgreSQL

This tests our abstractions without full complexity.

### Option 2: Extend Semantic Model First

Before adding Azure, extend the semantic model to support:

1. Event-driven scaling rules
2. Cloud-agnostic identity model
3. Cloud-agnostic secret references

Then implement Azure to validate the extensions work.

### Option 3: Azure-First Design

Design Azure support first, then retrofit AWS to fit:

- Container Apps' simpler model might be the better abstraction
- KEDA scaling is more flexible than target tracking
- Built-in ingress is simpler than ALB

## Questions for You

1. **Which Azure services?** Just Container Apps + Flexible Server? Or AKS for more control?

2. **Scaling priorities:** Is KEDA-style event scaling important, or HTTP scaling enough?

3. **Identity approach:** System-assigned (simpler) or user-assigned (more control)?

4. **Database:** Flexible Server (recommended) or Single Server (simpler API)?

5. **Scope:** Full feature parity with AWS, or MVP that proves the abstraction?

## My Recommendation

**Option 1: Minimal Azure MVP**

Because:
- Tests our abstractions without requiring major semantic changes
- Container Apps is genuinely different (higher-level, simpler)
- If the abstraction breaks, we'll know quickly
- Less code to maintain if we decide not to pursue Azure fully

The gaps we identify will inform whether we need Option 2 (extend semantic model) or if our current abstraction is wrong (Option 3).

What do you think? Should we proceed with a minimal Azure Container Apps implementation?
