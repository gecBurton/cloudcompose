# GCP Architecture Spike

**Objective**: Explore how GCP services differ from AWS/Azure and what that means for our architecture.  
**Outcome**: Identify where our abstractions might break or need extension.  
**No Implementation**: This is a thought exercise only.

---

## 1. GCP Core Services Overview

### 1.1 Compute: Cloud Run vs ECS Fargate / Container Apps

| Aspect | AWS ECS Fargate | Azure Container Apps | GCP Cloud Run |
|--------|-----------------|---------------------|---------------|
| **Abstraction level** | Medium (task definitions) | High (opinionated) | Very high (functions-as-containers) |
| **Scaling** | Target tracking | KEDA events | Request-based (automatic) |
| **Scale to zero** | ❌ No | ✅ Yes | ✅ Yes (native) |
| **Cold start** | ~30s | ~10s | ~2s |
| **Max concurrency** | 1 per task | Configurable | 1000 per instance |
| **Request timeout** | 60 min | Configurable | 60 min (3600s) |
| **CPU allocation** | Always allocated | Always allocated | Request-only (throttled) |
| **Private networking** | VPC | VNet | VPC + Serverless VPC Access |

**Key insight**: Cloud Run is more "function-like" than either ECS or Container Apps. It assumes HTTP request/response and scales to zero by default. This is fundamentally different from our "service" abstraction.

### 1.2 Database: Cloud SQL vs RDS / Flexible Server

| Aspect | AWS RDS | Azure Flexible | GCP Cloud SQL |
|--------|---------|----------------|---------------|
| **PostgreSQL** | ✅ Yes | ✅ Yes | ✅ Yes |
| **MySQL** | ✅ Yes | ✅ Yes | ✅ Yes |
| **SQL Server** | ✅ Yes | ❌ No | ❌ No |
| **High availability** | Multi-AZ | Zone redundant | Regional (cross-zone) |
| **Read replicas** | ✅ Cross-region | ✅ Same region | ✅ Cross-region |
| **Auto-scaling storage** | ✅ Yes | ✅ Yes | ✅ Yes |
| **Connection proxy** | RDS Proxy | Built-in (pgbouncer) | Cloud SQL Auth Proxy |
| **Private IP** | VPC | VNet | VPC + Private Service Connect |

**Key insight**: Cloud SQL is very similar to RDS and Azure Flexible. No major abstraction challenge here.

### 1.3 Cache: Memorystore vs ElastiCache / Azure Redis

| Aspect | AWS ElastiCache | Azure Cache | GCP Memorystore |
|--------|-----------------|-------------|-----------------|
| **Redis** | ✅ Yes | ✅ Yes | ✅ Yes |
| **Memcached** | ✅ Yes | ❌ No | ❌ No |
| **Cluster mode** | ✅ Yes | Premium only | ✅ Yes |
| **Persistence** | AOF/RDB | RDB/AOF | RDB/AOF |
| **Network** | Subnet groups | VNet injection | VPC + Private Service Connect |

**Key insight**: Memorystore is functionally equivalent. No abstraction challenge.

### 1.4 Storage: Cloud Storage vs S3 / Blob Storage

| Aspect | AWS S3 | Azure Blob | GCP Cloud Storage |
|--------|--------|------------|-------------------|
| **Object storage** | ✅ Yes | ✅ Yes | ✅ Yes |
| **Storage classes** | 6 tiers | 4 tiers | 4 tiers |
| **Lifecycle policies** | ✅ Yes | ✅ Yes | ✅ Yes |
| **Versioning** | ✅ Yes | ✅ Yes | ✅ Yes |
| **Signed URLs** | ✅ Yes | ✅ Yes | ✅ Yes |
| **Uniform bucket-level access** | Bucket policy | RBAC | ✅ Native (recommended) |

**Key insight**: Cloud Storage is very similar to S3. No major abstraction challenge.

### 1.5 CDN: Cloud CDN vs CloudFront / Azure CDN

| Aspect | AWS CloudFront | Azure CDN | GCP Cloud CDN |
|--------|----------------|-----------|---------------|
| **Global edge network** | ✅ Yes | ✅ Yes | ✅ Yes |
| **Origin types** | S3, ALB, custom | Storage, App Service, custom | GCLB, Cloud Storage, custom |
| **Signed URLs/cookies** | ✅ Yes | ✅ Yes | ✅ Yes |
| **WAF integration** | ✅ Yes | ✅ Yes | Cloud Armor (separate) |
| **Cache behaviors** | Detailed rules | Rules engine | URL maps |

**Key insight**: Cloud CDN requires a Global Load Balancer (GCLB) as the entry point. This is a significant architectural difference.

---

## 2. Architectural Challenges for GCP

### 2.1 Challenge 1: Request-Based vs Always-Running Services

**Current Abstraction**:
```yaml
services:
  web:
    x-composey:
      min_scale: 2  # Always at least 2 instances
      max_scale: 10
```

**GCP Reality**:
Cloud Run services scale to zero by default. You pay only for requests handled. This is fundamentally different from our "always-running service" model.

**Implications**:
- `min_scale: 0` is native to GCP but requires special config for AWS/Azure
- Cold starts are much faster on GCP (2s vs 30s)
- Our "service" abstraction assumes persistent instances

**Potential Solutions**:

1. **Embrace GCP model** (recommended for future)
   ```yaml
   x-composey:
     type: request_handler  # vs persistent_service
     concurrency: 1000  # requests per instance
   ```

2. **Force always-running** (current approach)
   - Set `min_scale: 1` on Cloud Run
   - Waste money on idle instances
   - Loses GCP's main benefit

### 2.2 Challenge 2: Concurrency Model

**Current Assumption**: One request per instance

**GCP Reality**: Cloud Run handles up to 1000 concurrent requests per instance

**Implications**:
- Our health checks assume "one health check = one instance"
- Memory/CPU calculations don't account for request multiplexing
- Load balancing is request-based, not connection-based

**Potential Solutions**:

```yaml
x-composey:
  concurrency:
    max_requests_per_instance: 1000  # GCP native
    max_connections: 1000  # AWS ALB
```

### 2.3 Challenge 3: No Native Service Discovery

**AWS**: CloudMap (DNS-based)  
**Azure**: Built-in (same Container Apps Environment)  
**GCP**: ❌ Nothing native

**GCP Options**:
1. **Cloud DNS + Cloud Service Directory** (complex, expensive)
2. **Environment variables** (static, no updates)
3. **Firestore/Cloud SQL as service registry** (custom, complex)

**Implications**:
- Our `network_isolation_segments` + service discovery pattern breaks
- Services can't easily find each other by name
- Need to inject connection strings at deploy time

**Potential Solutions**:

```yaml
# Option 1: Static injection (simple, works everywhere)
services:
  api:
    environment:
      DATABASE_URL: ${services.db.connection_string}

# Option 2: Embrace serverless (more radical)
# Services don't talk to each other directly
# Use Pub/Sub, Cloud Tasks, or Firestore as intermediary
```

### 2.4 Challenge 4: Global Load Balancer Required for CDN

**AWS**: CloudFront → ALB → ECS  
**Azure**: CDN → Container Apps (built-in)  
**GCP**: Cloud CDN → GCLB → Cloud Run

**Implications**:
- GCP requires an extra hop (GCLB)
- GCLB is global, not regional
- URL maps are different from ALB rules

**Potential Solutions**:

```yaml
x-composey:
  ingress:
    path: /api
    # AWS: ALB listener rule
    # Azure: Container Apps ingress
    # GCP: GCLB URL map path matcher
```

### 2.5 Challenge 5: IAM vs Service Accounts

**AWS**: IAM roles attached to tasks  
**Azure**: Managed identities  
**GCP**: Service accounts + IAM bindings

**GCP Complexity**:
- Service accounts are project-level
- Must grant permissions to each resource
- Cloud Run services need explicit IAM bindings

**Example**:
```yaml
# GCP requires explicit grants
- Grant service account 'Cloud SQL Client' role
- Grant service account 'Storage Object Viewer' role
- Grant service account 'Pub/Sub Publisher' role
```

**Implications**:
- Our `access` intent model needs expansion
- Need to map semantic permissions to GCP IAM roles

---

## 3. What This Means for Our Architecture

### 3.1 The "Service" Abstraction is Cloud-Dependent

Our current `Service` model assumes:
- Always-running instances
- One request per instance (mostly)
- DNS-based service discovery
- Load balancer as separate resource

**GCP challenges all of these**.

### 3.2 Two Possible Paths

#### Path A: Force GCP into Our Model (Short-term)

Keep current abstractions, configure GCP to match:

```yaml
# Force Cloud Run to behave like ECS
x-composey:
  min_scale: 1  # Never scale to zero
  cpu: "1"      # Always allocated
  execution_environment: "always_running"
```

**Pros:**
- ✅ No semantic model changes
- ✅ Consistent across clouds

**Cons:**
- ❌ Wastes GCP's main benefit (scale-to-zero)
- ❌ More expensive
- ❌ Slower deployments

#### Path B: Evolve the Abstraction (Long-term)

Redefine what a "service" means:

```yaml
services:
  # Type 1: Request Handler (GCP native)
  api:
    x-composey:
      type: request_handler
      concurrency: 1000
      scale_to_zero: true
      cold_start_max: 5s

  # Type 2: Persistent Service (AWS/Azure native)
  worker:
    x-composey:
      type: persistent_service
      min_replicas: 2
      max_replicas: 10

  # Type 3: Scheduled Job
  cleanup:
    x-composey:
      type: scheduled_job
      schedule: "0 2 * * *"
```

**Pros:**
- ✅ Leverages each cloud's strengths
- ✅ Cost-optimal
- ✅ Future-proof

**Cons:**
- ❌ More complex semantic model
- ❌ Harder to make cloud-agnostic
- ❌ Breaking changes

### 3.3 Service Discovery Needs Rethinking

**Current**: Services find each other via DNS (CloudMap, built-in)

**GCP Options**:

1. **Static injection** (simplest)
   ```yaml
   environment:
     DB_HOST: ${services.db.host}  # Injected at deploy
   ```

2. **Service mesh** (complex)
   - Anthos Service Mesh
   - Istio on GKE

3. **Event-driven** (radical)
   - Services don't call each other
   - Use Pub/Sub for all communication

**Recommendation**: Static injection for now, event-driven for future.

### 3.4 The "Ingress" Abstraction is Too AWS-Centric

**Current**:
```yaml
ingress:
  path: /api
  port: 8080
```

**Problems**:
- AWS ALB: Path-based routing
- Azure: Container Apps built-in
- GCP: Requires GCLB URL map

**Evolved**:
```yaml
x-composey:
  routing:
    type: http
    paths:
      - /api
      - /api/v2
    host: api.example.com  # Optional custom domain
    tls: true              # HTTPS
    
  # Cloud-specific overrides
  aws:
    alb_priority: 100
  azure:
    custom_domain_verification: txt-record
  gcp:
    url_map_host: api
```

---

## 4. Recommendations

### 4.1 Short-term (Next 3 months)

**Don't implement GCP yet.**

Instead:
1. **Harden AWS and Azure** with live tests
2. **Collect real usage data** - which abstractions work, which don't
3. **Document intent-based patterns** - what do users actually need?

### 4.2 Medium-term (3-6 months)

**Extend semantic model** to support service types:

```python
class ServiceType(Enum):
    PERSISTENT = "persistent"      # AWS ECS, Azure Container Apps
    REQUEST_HANDLER = "request"    # GCP Cloud Run, AWS Lambda
    SCHEDULED_JOB = "scheduled"    # AWS EventBridge, Azure Container Apps Jobs
    STREAM_PROCESSOR = "stream"    # AWS Kinesis, GCP Dataflow
```

### 4.3 Long-term (6-12 months)

**Implement GCP** with evolved abstractions:

1. Refactor `Service` to support multiple types
2. Implement static service discovery
3. Add GCLB + Cloud CDN support
4. Create GCP-specific inference module

---

## 5. Key Takeaways

### 5.1 GCP Would Force Hard Questions

1. **What is a "service"?** Always-running vs request-handler
2. **How do services communicate?** Direct vs event-driven
3. **What does "ingress" mean?** Load balancer vs URL map vs built-in
4. **How do we handle IAM?** Roles vs identities vs service accounts

### 5.2 Our Current Abstractions Are AWS-Centric

- `min_scale` / `max_scale` assumes persistent instances
- `network_isolation_segments` assumes VPC-style networking
- `ingress` assumes load balancer architecture

### 5.3 The Value Proposition Shifts

**AWS/Azure**: "Same docker-compose runs on either cloud"  
**GCP**: "Optimize for each cloud's strengths"

With GCP, we'd be offering:
- Scale-to-zero for cost savings
- Request-based concurrency for efficiency
- Event-driven architecture for decoupling

This is a **different value proposition** than "lift and shift."

---

## 6. Conclusion

**GCP is the canary in the coal mine.**

If our abstractions can't handle GCP's request-based, scale-to-zero model, they won't handle:
- AWS Lambda
- Azure Container Apps Jobs
- Knative
- Any future serverless platform

**Recommendation**: Treat GCP as a design exercise. Implement it only after we've evolved the semantic model to support multiple service types.

The question isn't "Can we support GCP?" but "Should our abstractions change to better support the future of serverless?"
