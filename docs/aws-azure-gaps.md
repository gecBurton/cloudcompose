# AWS vs Azure Feature Gap Analysis

This document compares the current feature support between AWS and Azure implementations.

## Summary Table

| Feature Category | Feature | AWS | Azure | Notes |
|-----------------|---------|-----|-------|-------|
| **Compute** | Serverless containers | ✅ ECS Fargate | ✅ Container Apps | Both supported |
| | Task definitions | ✅ Detailed | ⚠️ Simplified | Azure uses simpler container spec |
| | Service discovery | ✅ CloudMap | ⚠️ Built-in | Azure has automatic discovery |
| | Build from source | ✅ ECR | ✅ ACR | Both supported |
| **Scaling** | CPU-based | ✅ Target tracking | ✅ KEDA | Both supported |
| | Memory-based | ✅ Target tracking | ⚠️ Not implemented | Azure can add KEDA memory trigger |
| | Request-based | ✅ ALB requests | ✅ HTTP concurrent | Different mechanisms |
| | Queue-based | ❌ Not implemented | ❌ Not implemented | Future enhancement |
| | Custom metrics | ❌ Not implemented | ❌ Not implemented | Future enhancement |
| | Step scaling | ❌ Not implemented | ❌ Not implemented | Future enhancement |
| **Database** | PostgreSQL | ✅ RDS | ✅ Flexible Server | Both supported |
| | MySQL | ✅ RDS | ❌ Not implemented | Can be added |
| | Read replicas | ❌ Not implemented | ❌ Not implemented | Future enhancement |
| | Auto-scaling storage | ✅ | ✅ | Azure Flexible Server has this |
| | Connection pooling | ❌ Not implemented | ✅ Built-in | Azure has pgbouncer |
| **Cache** | Redis | ✅ ElastiCache | ❌ Not implemented | Azure Cache for Redis needed |
| | Cluster mode | ✅ | ❌ Not implemented | Azure Premium tier |
| **Storage** | Object storage | ✅ S3 | ❌ Not implemented | Azure Blob Storage needed |
| | CDN | ✅ CloudFront | ❌ Not implemented | Azure CDN / Front Door needed |
| | WAF | ✅ WAFv2 | ❌ Not implemented | Azure Application Gateway WAF needed |
| **Networking** | Load balancer | ✅ ALB | ⚠️ Built-in | Azure doesn't need separate ALB |
| | Security groups | ✅ Detailed | ⚠️ Simplified | Azure uses VNet integration |
| | Custom rules | ✅ | ❌ Not implemented | Azure NSGs not implemented |
| **Security** | Secrets | ✅ Secrets Manager | ✅ Key Vault | Both supported |
| | IAM roles | ✅ Detailed | ⚠️ Simplified | Azure uses managed identities |
| | Fine-grained policies | ✅ | ❌ Not implemented | Azure RBAC not fully implemented |
| **Observability** | Logs | ✅ CloudWatch | ⚠️ Log Analytics | Basic support |
| | Metrics | ✅ CloudWatch | ❌ Not implemented | Azure Monitor needed |
| | Alarms | ❌ Not implemented | ❌ Not implemented | Future enhancement |
| **Scheduling** | Cron jobs | ✅ EventBridge | ❌ Not implemented | Azure Container Apps Jobs needed |
| | Rate-based | ✅ EventBridge | ❌ Not implemented | Future enhancement |

## Detailed Gap Analysis

### 🔴 Critical Gaps (Blocking Production Use)

#### 1. **Redis/Cache Support (Azure)**
**Impact**: Many applications use Redis for caching/sessions
**AWS**: ✅ ElastiCache (Redis) fully supported
**Azure**: ❌ Not implemented
**Solution**: Add Azure Cache for Redis support

```python
# Needed in azure.py
class RedisCache(BaseModel):
    name: str
    resource_group_name: str
    location: str
    capacity: int  # 0-6 for Basic/Standard, 1-5 for Premium
    family: str    # "C" or "P"
    sku_name: str  # "Basic", "Standard", "Premium"
    enable_non_ssl_port: bool = False
    minimum_tls_version: str = "1.2"
```

#### 2. **Object Storage/S3 Equivalent (Azure)**
**Impact**: File uploads, static assets, backups
**AWS**: ✅ S3 fully supported
**Azure**: ❌ Not implemented
**Solution**: Add Azure Blob Storage support

```python
# Needed in azure.py
class StorageAccount(BaseModel):
    name: str
    resource_group_name: str
    location: str
    account_tier: str = "Standard"
    account_replication_type: str = "LRS"
    
class StorageContainer(BaseModel):
    name: str
    storage_account_name: str
    container_access_type: str = "private"
```

#### 3. **MySQL Support (Azure)**
**Impact**: Applications using MySQL instead of PostgreSQL
**AWS**: ✅ RDS MySQL fully supported
**Azure**: ❌ Not implemented
**Solution**: Add Azure Database for MySQL Flexible Server

### 🟡 Important Gaps (Reduced Functionality)

#### 4. **Memory-Based Auto-Scaling (Azure)**
**Impact**: Can't scale based on memory pressure
**AWS**: ✅ Target tracking on memory
**Azure**: ⚠️ Only HTTP scaling implemented
**Solution**: Add KEDA memory trigger

```python
# In azure/__init__.py _infer_container_apps()
if metric.type == "memory":
    scale_rules.append({
        "name": "memory-rule",
        "custom": {
            "type": "memory",
            "metadata": {
                "type": "Utilization",
                "value": str(int(metric.target_value)),
            },
        },
    })
```

#### 5. **Scheduled Tasks/Cron Jobs (Azure)**
**Impact**: Background jobs, batch processing
**AWS**: ✅ EventBridge + ECS tasks
**Azure**: ❌ Not implemented
**Solution**: Azure Container Apps Jobs or Logic Apps

#### 6. **CDN Support (Azure)**
**Impact**: Static asset delivery, edge caching
**AWS**: ✅ CloudFront with WAF
**Azure**: ❌ Not implemented
**Solution**: Azure CDN or Front Door

### 🟢 Minor Gaps (Nice to Have)

#### 7. **Read Replicas (Both)**
**Impact**: Read scaling for databases
**AWS**: ❌ Not implemented
**Azure**: ❌ Not implemented
**Priority**: Medium

#### 8. **CloudWatch/Monitoring (Azure)**
**Impact**: Observability, dashboards
**AWS**: ⚠️ Basic (Log Groups)
**Azure**: ❌ Not implemented
**Priority**: Low

#### 9. **Fine-Grained IAM/Policies (Azure)**
**Impact**: Security hardening
**AWS**: ✅ Detailed IAM roles and policies
**Azure**: ⚠️ Basic managed identity
**Priority**: Low

## Semantic Model Gaps

Some features can't be implemented because the semantic model doesn't support them:

### Missing Abstractions

1. **Queue-Based Scaling**
   ```python
   # Proposed addition to semantic model
   class QueueScaling(BaseModel):
       queue_name: str
       queue_type: Literal["sqs", "servicebus", "rabbitmq"]
       target_depth: int
   ```

2. **Cache Configuration**
   ```python
   # Proposed addition
   class CacheConfig(BaseModel):
       ttl: int
       cluster_mode: bool
       persistence: bool
   ```

3. **Storage Lifecycle**
   ```python
   # Proposed addition
   class StorageLifecycle(BaseModel):
       transition_to_ia_days: int
       transition_to_glacier_days: int
       expiration_days: int
   ```

## Recommendations

### Short Term (Next 2-4 Weeks)

1. **Azure Redis Cache** - High impact, relatively simple
2. **Azure Blob Storage** - High impact, needed for many apps
3. **Azure MySQL** - Medium impact, straightforward

### Medium Term (1-2 Months)

4. **Memory scaling for Azure** - Small change, big improvement
5. **Scheduled tasks for Azure** - Container Apps Jobs
6. **Read replicas** - Both clouds

### Long Term (2-3 Months)

7. **CDN support** - Both clouds
8. **Queue-based scaling** - Requires semantic model changes
9. **Advanced monitoring** - Both clouds

## Usage Comparison

### Current AWS Usage
```yaml
# Full-featured AWS deployment
services:
  web:
    image: myapp
    x-composey:
      size: large
      ingress: {}
      min_scale: 2
      max_scale: 20
      auto_scaling:
        metrics:
          - type: cpu
            target: 70
          - type: memory
            target: 80
          - type: requests_per_target
            target: 1000
  
  cache:
    image: redis
    x-composey:
      capability: cache
  
  storage:
    image: minio
    x-composey:
      capability: object-storage
```

### Current Azure Limitations
```yaml
# Limited Azure deployment
services:
  web:
    image: myapp
    x-composey:
      size: large
      ingress: {}
      min_scale: 2
      max_scale: 20
      auto_scaling:
        metrics:
          - type: cpu
            target: 70
          # Memory scaling not implemented
          # requests_per_target becomes HTTP concurrent
  
  # cache: NOT SUPPORTED
  # storage: NOT SUPPORTED
  
  db:
    image: postgres
    x-composey:
      capability: database
      # Only PostgreSQL, not MySQL
```

## Conclusion

**Azure MVP Status**: ✅ Functional for basic web applications
**Production Ready**: ❌ Missing cache, storage, MySQL
**Priority**: Add Redis and Blob Storage to reach feature parity with core AWS features.
