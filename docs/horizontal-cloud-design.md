# Horizontal Cloud Design: Evolving Toward Serverless Abstractions

**Goal**: Redesign AWS and Azure implementations to support serverless patterns (like GCP), validating that our abstractions work across all three clouds.

**Philosophy**: Design for the future (serverless) and backport to traditional clouds, rather than designing for traditional clouds and trying to fit serverless later.

---

## 1. The Core Insight

**GCP's model is the future:**
- Scale-to-zero (pay only for what you use)
- Request-based concurrency (efficient resource utilization)
- Fast cold starts (2 seconds, not 30)
- Built-in ingress (no separate load balancer management)

**Traditional clouds can emulate this:**
- AWS: Lambda (functions) + Fargate (always-on for long-running)
- Azure: Container Apps (built-in scale-to-zero)

---

## 2. Redesigned Service Model

### 2.1 Service Types

Replace `min_scale`/`max_scale` with explicit service types:

```yaml
services:
  # Type 1: HTTP Request Handler
  # Scales to zero, request-based billing, fast cold start
  api:
    image: myapp/api
    x-composey:
      type: http_service
      routes:
        - path: /api
          methods: [GET, POST]
      concurrency:
        max_requests: 100        # Per instance
      scaling:
        min_instances: 0         # Scale to zero (default)
        max_instances: 100
        target_cpu: 70
        target_requests: 1000    # Requests per instance

  # Type 2: Background Worker
  # Event-driven, scales to zero, processes from queue
  worker:
    image: myapp/worker
    x-composey:
      type: event_processor
      triggers:
        - type: queue
          source: my-queue       # SQS / Service Bus / Pub/Sub
          batch_size: 10
      scaling:
        min_instances: 0
        max_instances: 50

  # Type 3: Scheduled Job
  # Runs on schedule, scales to zero between runs
  cleanup:
    image: myapp/cleanup
    x-composey:
      type: scheduled_task
      schedule: "0 2 * * *"      # Cron expression
      timeout: 3600              # 1 hour max

  # Type 4: Persistent Service
  # Always-on, for stateful or long-running processes
  websocket:
    image: myapp/ws
    x-composey:
      type: persistent_service   # Legacy mode
      scaling:
        min_instances: 2         # Always at least 2
        max_instances: 10
```

### 2.2 Cloud-Specific Implementation

#### HTTP Service

**GCP (Cloud Run)** - Native:
```yaml
# Maps directly to Cloud Run
# Scale-to-zero: native
# Concurrency: native (up to 1000)
# Ingress: built-in HTTPS
```

**AWS** - Fargate + Application Autoscaling:
```yaml
# Fargate with target tracking
# Custom metric: ALB RequestCountPerTarget
# Scale-to-zero: EventBridge rule to stop service when idle
# Challenge: Cold start is 30s vs 2s
```

**Azure** - Container Apps:
```yaml
# Container Apps with KEDA HTTP trigger
# Scale-to-zero: native
# Concurrency: KEDA http concurrentRequests
# Almost identical to GCP!
```

#### Event Processor

**GCP** - Cloud Run + Pub/Sub:
```yaml
# Cloud Run triggered by Pub/Sub push subscription
# Scale-to-zero: native
# Concurrency: native
```

**AWS** - Lambda (short) / Fargate (long):
```yaml
# Lambda for <15 min tasks
# OR Fargate + SQS for long-running
# Scale-to-zero: Lambda native, Fargate requires custom logic
```

**Azure** - Container Apps + KEDA:
```yaml
# KEDA Azure Service Bus trigger
# Scale-to-zero: native
# Concurrency: configurable
```

#### Scheduled Task

**GCP** - Cloud Scheduler + Cloud Run:
```yaml
# Cloud Scheduler HTTP target to Cloud Run
# Scale-to-zero: native (invokes on schedule)
```

**AWS** - EventBridge + Fargate:
```yaml
# EventBridge rule triggers ECS task
# Scale-to-zero: task runs then stops
```

**Azure** - Container Apps Jobs (preview) / Logic Apps:
```yaml
# Container Apps Jobs with schedule trigger
# OR Logic Apps with HTTP call
```

---

## 3. Redesigned Service Discovery

### 3.1 The Problem

**Current**: DNS-based discovery (CloudMap, built-in Azure)

**GCP Challenge**: No native service discovery

### 3.2 Solution: Intent-Based Connections

Replace implicit DNS discovery with explicit connection declarations:

```yaml
services:
  api:
    image: myapp/api
    x-composey:
      type: http_service
    connections:
      - target: database
        type: database
        env_var: DATABASE_URL
      - target: cache
        type: cache
        env_var: REDIS_URL
      - target: storage
        type: storage
        permissions: [read, write]

database:
  image: postgres:15
  x-composey:
    capability: database
    # Exposes connection string to dependent services
```

### 3.3 Implementation by Cloud

**GCP**:
- Compile-time injection of connection strings
- Cloud Run environment variables
- No runtime discovery needed

**AWS**:
- Option 1: Compile-time injection (simpler)
- Option 2: CloudMap + SSM Parameter Store (complex)

**Azure**:
- Option 1: Compile-time injection
- Option 2: Service discovery via Container Apps environment

### 3.4 Benefits

1. **Works on all clouds** - No reliance on cloud-specific discovery
2. **Explicit dependencies** - Clear graph of service relationships
3. **Security by default** - Only declared connections are allowed
4. **Testable locally** - Connection strings can be localhost in dev

---

## 4. Redesigned Ingress/Routing

### 4.1 The Problem

**Current**: Path-based routing on a shared load balancer

**GCP Reality**: Each service has its own HTTPS URL

### 4.2 Solution: API Gateway / Front Door Pattern

```yaml
x-composey:
  gateway:
    type: http                    # http, grpc, websocket
    domains:
      - api.example.com
      - www.example.com
    routes:
      - path: /api/v1/users
        service: api
        rewrite: /users           # Strip /api/v1
        
      - path: /api/v2/*
        service: api-v2
        
      - path: /static/*
        service: cdn
        cache_ttl: 86400          # 1 day
        
      - path: /ws
        service: websocket
        protocol: websocket
    
    tls:
      mode: terminate             # terminate, passthrough
      certificate: managed        # managed, custom
    
    auth:
      type: jwt                   # jwt, oauth, api_key
      issuer: https://auth.example.com
```

### 4.3 Implementation by Cloud

**GCP**:
- Cloud Load Balancing (GCLB) with URL maps
- Cloud CDN for caching
- Cloud Armor for security

**AWS**:
- API Gateway v2 (HTTP API) for routing
- CloudFront for CDN
- Application Load Balancer (fallback)

**Azure**:
- API Management (heavy) OR
- Application Gateway + Front Door

---

## 5. Implementation Roadmap

### Phase 1: Refactor Abstractions (Weeks 1-2)

**Semantic Model Changes**:
```python
class ServiceType(str, Enum):
    HTTP_SERVICE = "http_service"
    EVENT_PROCESSOR = "event_processor"
    SCHEDULED_TASK = "scheduled_task"
    PERSISTENT_SERVICE = "persistent_service"

class Service(BaseModel):
    name: str
    type: ServiceType
    connections: List[Connection] = []
    # Remove: min_scale, max_scale (moved to type-specific config)
```

**Backwards Compatibility**:
- Old `min_scale`/`max_scale` maps to `persistent_service`
- Deprecation warning, but still works

### Phase 2: Implement Azure First (Weeks 3-4)

**Why Azure first?**
- Container Apps is closest to GCP model
- Native scale-to-zero
- KEDA event triggers
- Easiest migration path

**Steps**:
1. Refactor Azure inference for service types
2. Implement HTTP service with KEDA
3. Implement event processor with KEDA
4. Implement scheduled task with Container Apps Jobs

### Phase 3: Implement GCP (Weeks 5-6)

**Steps**:
1. Create GCP inference module
2. Implement HTTP service with Cloud Run
3. Implement event processor with Pub/Sub
4. Implement scheduled task with Cloud Scheduler

### Phase 4: Refactor AWS (Weeks 7-8)

**Challenge**: AWS is the hardest to make serverless

**Strategy**:
1. HTTP service: Fargate + aggressive autoscaling (emulate scale-to-zero)
2. Event processor: Lambda (short) / Fargate (long)
3. Scheduled task: EventBridge + Fargate
4. Persistent service: Current ECS implementation

**Note**: AWS will be less "serverless" than GCP/Azure, but that's AWS's limitation, not ours.

### Phase 5: Unify and Test (Weeks 9-10)

1. Create cross-cloud examples
2. Run same compose file on all three clouds
3. Verify equivalent behavior
4. Performance comparison
5. Cost comparison

---

## 6. Example: Same Compose, Three Clouds

### Compose File

```yaml
version: "3.8"

services:
  api:
    image: myapp/api:v1
    x-composey:
      type: http_service
      routes:
        - path: /api
      concurrency:
        max_requests: 100
      scaling:
        min_instances: 0
        max_instances: 50
    connections:
      - target: db
        type: database
      - target: cache
        type: cache

  worker:
    image: myapp/worker:v1
    x-composey:
      type: event_processor
      triggers:
        - type: queue
          source: jobs-queue
      scaling:
        min_instances: 0
        max_instances: 20
    connections:
      - target: db
        type: database

  cleanup:
    image: myapp/cleanup:v1
    x-composey:
      type: scheduled_task
      schedule: "0 2 * * *"
    connections:
      - target: db
        type: database

db:
  image: postgres:15
  x-composey:
    capability: database

cache:
  image: redis:7
  x-composey:
    capability: cache
```

### AWS Result

```
API: ECS Fargate + ALB + Target Tracking Autoscaling
     (Scales to 0 via EventBridge when idle)
     
Worker: Lambda (if <15min) OR Fargate + SQS
        (Lambda scales to 0, Fargate requires custom logic)
        
Cleanup: EventBridge Rule → ECS Task
         (Task runs then stops)
```

### Azure Result

```
API: Container Apps + KEDA HTTP trigger
     (Native scale-to-zero)
     
Worker: Container Apps + KEDA Azure Service Bus trigger
        (Native scale-to-zero)
        
Cleanup: Container Apps Jobs + Schedule trigger
         (Native scheduled execution)
```

### GCP Result

```
API: Cloud Run
     (Native scale-to-zero, request-based)
     
Worker: Cloud Run + Pub/Sub push subscription
        (Native event-driven, scale-to-zero)
        
Cleanup: Cloud Scheduler → Cloud Run
         (Native scheduled execution)
```

---

## 7. Benefits of This Approach

### 7.1 For Users

1. **Cost optimization** - Scale-to-zero on all clouds
2. **Faster deployments** - No load balancer management for simple cases
3. **Clearer intent** - Explicit service types and connections
4. **Cloud-agnostic** - Same compose file, optimal implementation on each cloud

### 7.2 For Development

1. **Focused implementation** - Each service type has clear requirements
2. **Testable abstractions** - Can test "HTTP service" behavior independent of cloud
3. **Extensible** - Easy to add new service types (WebSocket, gRPC, etc.)
4. **Future-proof** - Ready for emerging serverless platforms

### 7.3 For Business

1. **Competitive advantage** - True multi-cloud with optimal implementations
2. **Cost leadership** - Serverless is cheaper for most workloads
3. **Developer experience** - Modern, serverless-native abstractions
4. **Risk mitigation** - Not tied to any single cloud's architecture

---

## 8. Risks and Mitigations

### 8.1 Risk: AWS Implementation is "Fake" Serverless

**Concern**: AWS Fargate can't truly scale to zero like GCP/Azure

**Mitigation**:
- Document the limitation honestly
- Provide Lambda option for true serverless (<15min tasks)
- Offer "bring your own load balancer" for traditional deployments
- Let users choose: serverless (limited) vs traditional (full features)

### 8.2 Risk: Breaking Changes

**Concern**: Existing users depend on current abstractions

**Mitigation**:
- `persistent_service` type maintains current behavior
- Deprecation warnings with migration guide
- Gradual rollout (Azure first, then GCP, then AWS refactor)

### 8.3 Risk: Complexity

**Concern**: More service types = more complexity

**Mitigation**:
- Sensible defaults (HTTP service is default for containers with ports)
- Good documentation and examples
- CLI validation and helpful error messages

---

## 9. Conclusion

**The horizontal approach is correct.**

By designing for GCP's serverless model and backporting to AWS/Azure, we:

1. **Future-proof** the architecture
2. **Optimize costs** on all clouds
3. **Simplify** the mental model (intent over implementation)
4. **Enable** true multi-cloud portability

**The alternative** (optimizing for AWS and trying to fit others) leads to:
- Wasteful resource usage
- Complex abstraction layers
- Vendor lock-in
- Technical debt

**Recommendation**: Implement this redesign. Start with Azure (closest to ideal), then GCP (native), then refactor AWS (emulation).
