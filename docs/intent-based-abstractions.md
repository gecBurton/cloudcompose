# Intent-Based Abstractions for Multi-Cloud

The docker-compose.yml captures **application intent**. The compiler translates intent to cloud-specific implementation. Engineers write the same compose file regardless of target cloud.

## Guiding Principle

**Compose file declares:** "I need this to handle 1000 concurrent users"
**Compiler decides:** Target tracking (AWS) vs KEDA HTTP scaling (Azure)

**Compose file declares:** "These services should talk to each other"
**Compiler decides:** Security groups (AWS) vs VNet integration (Azure)

## Intent-Based Abstractions

### 1. Scaling Intent

**NOT this (implementation-specific):**
```yaml
# AWS-specific
auto_scaling:
  metrics:
    - type: cpu
      target: 70

# Azure-specific
auto_scaling:
  rules:
    - type: http
      metadata:
        concurrentRequests: "50"
```

**BUT this (intent-based):**
```yaml
x-composey:
  capacity:
    # Intent: Handle load with acceptable latency
    min_replicas: 2
    max_replicas: 10
    
    # Intent: Scale based on load
    scale_on:
      - load: cpu_high  # "I'm CPU-bound"
        threshold: 70   # "At 70% I need more capacity"
      
      - load: memory_high  # "I'm memory-bound"
        threshold: 80
      
      - load: requests_high  # "I'm request-bound"
        threshold: 1000  # requests per instance
```

**Compiler translates:**
- AWS: Target tracking policies (CPU/memory ALB request count)
- Azure: KEDA triggers (CPU/memory/HTTP concurrent requests)
- GCP: Autoscaling metrics (CPU/memory/request count)

### 2. Communication Intent

**NOT this:**
```yaml
# AWS-specific
networks:
  - backend
security_groups:
  - sg-123

# Azure-specific  
vnet_integration:
  vnet_id: "/subscriptions/..."
  subnet_id: "/subscriptions/..."
```

**BUT this:**
```yaml
services:
  api:
    networks:
      - backend
    # Intent: "I need to receive traffic from the internet"
    
  worker:
    networks:
      - backend
    # Intent: "I only talk to other services, not the internet"
    
  database:
    # No networks = isolated
    # Intent: "I only accept connections from specific services"
```

**Compiler translates:**
- AWS: Security groups, ingress rules
- Azure: VNet integration, Container Apps environment
- GCP: VPC connectors, firewall rules

### 3. Identity Intent

**NOT this:**
```yaml
# AWS-specific
iam_role: arn:aws:iam::123:role/my-role

# Azure-specific
managed_identity: /subscriptions/.../identities/my-id
```

**BUT this:**
```yaml
services:
  api:
    x-composey:
      access:
        # Intent: "I need to read from object storage"
        - resource: storage
          permissions: [read, write]
        
        # Intent: "I need to connect to the database"
        - resource: database
          permissions: [connect]
```

**Compiler translates:**
- AWS: IAM roles with specific policies
- Azure: Managed identities with role assignments
- GCP: Service accounts with IAM bindings

### 4. Secret Intent

**NOT this:**
```yaml
# AWS-specific
secrets:
  - source: my-secret
    valueFrom: arn:aws:secretsmanager:...

# Azure-specific
secrets:
  - source: my-secret
    keyVaultUrl: https://myvault.vault.azure.net/...
```

**BUT this (standard compose):**
```yaml
services:
  api:
    secrets:
      - db_password  # Intent: "I need the database password"
      - api_key      # Intent: "I need the API key"

secrets:
  db_password:
    external: true  # "Platform provides this"
  api_key:
    external: true
```

**Compiler translates:**
- AWS: Secrets Manager references + IAM permissions
- Azure: Key Vault references + managed identity
- GCP: Secret Manager references + service account

## Implementation: Azure Container Apps

### Environment Model

```python
class AzureEnvironment(BaseEnvironment):
    target: Literal["azure"] = "azure"
    region: str = "eastus"
    
    # Container Apps Environment (maps network isolation)
    container_apps_environment_name: str
    
    # VNet (maps to network isolation segments)
    vnet_id: str
    infrastructure_subnet_id: str
    
    # Log Analytics (Azure-specific requirement)
    log_analytics_workspace_id: str
    
    # Database (existing or new)
    postgresql_flexible_server_id: Optional[str]
```

### Inference: Container Apps

```python
def infer_container_apps(
    resources: AzureResources,
    app: SemanticApp,
    env: AzureEnvironment,
) -> None:
    for service in app.services:
        if service.capability != "container":
            continue
            
        # Intent: "I need X to Y replicas"
        # Azure: Container Apps with minReplicas/maxReplicas
        
        # Intent: "Scale based on load"
        # Azure: KEDA HTTP trigger (always for web)
        # Azure: CPU/Memory triggers (always for all)
        
        # Intent: "I receive internet traffic"
        # Azure: External ingress (if ingress declared)
        # Azure: Internal ingress (if no ingress but has port)
        
        # Intent: "I talk to other services"
        # Azure: Same Container Apps Environment (automatic)
```

### Inference: Flexible Server

```python
def infer_postgres_flexible(
    resources: AzureResources,
    service: SemanticService,
    env: AzureEnvironment,
) -> None:
    # Intent: "I need a PostgreSQL database"
    # Azure: Flexible Server
    
    # Intent: "High availability"
    # Azure: Zone redundant HA (optional)
    
    # Intent: "Read scaling"
    # Azure: Read replicas (optional)
```

## Compose File (Same for All Clouds)

```yaml
version: "3.8"

services:
  web:
    image: myapp/web:latest
    ports:
      - "80:8080"
    x-composey:
      size: medium
      ingress: {}  # Intent: "I need to be reachable from outside"
      capacity:
        min_replicas: 2
        max_replicas: 10
        scale_on:
          - load: cpu_high
            threshold: 70
          - load: requests_high
            threshold: 1000
    environment:
      DATABASE_URL: postgres://db:5432/mydb
    secrets:
      - db_password
    networks:
      - backend

  worker:
    image: myapp/worker:latest
    x-composey:
      size: small
      capacity:
        min_replicas: 1
        max_replicas: 5
        scale_on:
          - load: queue_depth  # Intent: "Scale based on work queue"
            threshold: 100
    networks:
      - backend

  db:
    image: postgres:15
    x-composey:
      capability: database
      # Intent: "I need a managed database"

networks:
  backend:
```

## Compiler Decisions

### AWS Path

```python
# Container: ECS Fargate
# - Task definition with CPU/memory from size
# - Service with target tracking autoscaling
# - ALB with listener rules
# - CloudMap for service discovery
# - Security groups from networks

# Database: RDS
# - DB instance with allocated storage
# - Subnet group
# - Security group
```

### Azure Path

```python
# Container: Container Apps
# - Container App with CPU/memory from size
# - KEDA HTTP trigger (if ingress)
# - KEDA CPU/memory triggers (always)
# - Built-in ingress (external or internal)
# - Automatic service discovery (same environment)
# - VNet integration

# Database: Flexible Server
# - Compute + storage (decoupled)
# - VNet integration
# - High availability (if multi-az requested)
```

## Benefits

1. **Same compose file** works on any cloud
2. **Intent is clear** - what the app needs, not how to get it
3. **Cloud optimizes** - each backend uses best practices for that cloud
4. **Easy migration** - change target, same source

## Trade-offs

1. **Less control** - Can't specify cloud-specific optimizations
2. **Different behavior** - Scaling may behave differently (target tracking vs KEDA)
3. **Feature gaps** - Some features may not map cleanly

## Recommendation

**Start with simple intent-based abstractions:**

1. **Capacity**: min/max replicas, scale triggers (cpu_high, memory_high, requests_high, queue_depth)
2. **Communication**: networks imply connectivity
3. **Access**: declarative permissions (read storage, connect to database)
4. **Secrets**: standard compose secrets (external = platform provides)

**Azure implementation:**
- Container Apps for compute (simplest, highest-level)
- Flexible Server for PostgreSQL
- KEDA HTTP triggers (don't implement queue scaling yet)
- System-assigned managed identities (simpler than user-assigned)

This proves the abstraction works without full feature parity.
