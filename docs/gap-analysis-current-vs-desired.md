# Gap Analysis: Current State vs. Desired State

**Analysis Date**: Current  
**Purpose**: Identify what needs to be built to achieve the core mission

---

## 1. The Core Mission (Desired State)

> **Docker Compose simplicity for cloud deployment.**

**The Experience:**
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

**User Mental Model:**
- I have services
- They need to talk to each other
- Some need to be public
- That's it

**User Does NOT Think About:**
- VPCs, subnets, CIDR blocks
- Load balancers, target groups
- IAM roles, policies
- Auto-scaling algorithms
- Certificate management
- Cost optimization strategies

---

## 2. Current State (What We Have)

### 2.1 What's Working ✅

#### Compiler Pipeline
| Stage | Status | Notes |
|-------|--------|-------|
| **Parse** | ✅ Complete | Docker Compose parsing |
| **Normalize** | ✅ Complete | Semantic model |
| **Infer (AWS)** | ✅ Complete | ECS, RDS, ElastiCache, S3, ALB |
| **Infer (Azure)** | ✅ Complete | Container Apps, Flexible Server, Cache, Blob, CDN |
| **Generate** | ✅ Complete | Terraform JSON for AWS and Azure |

#### Features Implemented
| Feature | AWS | Azure | Status |
|---------|-----|-------|--------|
| Container compute | ECS Fargate | Container Apps | ✅ Both |
| HTTP ingress | ALB | Built-in | ✅ Both |
| Auto-scaling | Target tracking | KEDA | ✅ Both |
| PostgreSQL | RDS | Flexible Server | ✅ Both |
| MySQL | RDS | Flexible Server | ✅ Both |
| Redis | ElastiCache | Cache for Redis | ✅ Both |
| Object Storage | S3 | Blob Storage | ✅ Both |
| CDN | CloudFront | CDN | ✅ Both |
| Secrets | Secrets Manager | Key Vault | ✅ Both |
| Service discovery | CloudMap | Built-in | ✅ Both |

#### Testing
| Type | AWS | Azure | Status |
|------|-----|-------|--------|
| Unit tests | ✅ ~200 | ✅ 13 | Good |
| Golden tests | ✅ 13 | ✅ 5 | Good |
| Integration tests | ✅ LocalStack | ❌ None | Gap |
| Live tests | ✅ Yes | ❌ None | Gap |

### 2.2 What's Partially Working ⚠️

#### CLI Experience
- Current: `uv run composey -f compose.yml -e env.yaml`
- Desired: `composey up --provider aws`
- **Gap**: CLI is clunky, requires explicit env file

#### Service Discovery
- Current: Compile-time injection for Azure
- Desired: Runtime service discovery (like Docker Compose)
- **Gap**: AWS uses CloudMap (good), Azure is basic

#### WebSockets
- Current: Works on AWS (ECS), limited on Azure
- Desired: Just works (like local Docker Compose)
- **Gap**: No clear abstraction for persistent connections

### 2.3 What's Missing ❌

#### GCP Support
- Status: ❌ Not implemented
- Priority: High (completes multi-cloud)
- Complexity: Medium (validates abstractions)

#### Live Testing (Azure)
- Status: ❌ Not implemented
- Priority: High (production confidence)
- Blocker: Azure subscription + setup

#### Simple CLI
- Current: Complex flags and env files
- Desired: `composey up --provider aws`
- **Gap**: User experience not polished

#### Smart Defaults
- Current: Requires explicit configuration
- Desired: Infer everything from docker-compose.yml
- **Gap**: Still need x-composey hints too often

#### Cost Optimization
- Current: Basic auto-scaling
- Desired: Scale-to-zero by default, optimize for cost
- **Gap**: Not serverless-first

---

## 3. The Gaps

### 3.1 Critical Gaps (Blocking Production Use)

#### Gap 1: GCP Support

**Why Critical:**
- Validates our abstractions work across all major clouds
- GCP forces us to handle serverless properly (Cloud Run)
- Completes the "any cloud" promise

**What Needs to Be Built:**
```python
# New module: composey/compiler/inference/gcp/
- __init__.py          # Main GCP inference
- _cloudrun.py         # Cloud Run services
- _cloudsql.py         # Cloud SQL databases
- _memorystore.py      # Redis
- _storage.py          # Cloud Storage
- _cdn.py              # Cloud CDN + GCLB
```

**Estimated Effort:** 2-3 weeks

---

#### Gap 2: Simple CLI Experience

**Current:**
```bash
uv run composey -f compose.yml -e env.yaml -p myapp -o build/
cd build && terraform init && terraform apply
```

**Desired:**
```bash
composey up --provider aws
```

**What Needs to Be Built:**
```python
# New CLI commands
composey up      # Deploy (infer provider, auto-apply)
composey down    # Destroy
composey status  # Show deployed services
composey logs    # Stream logs
composey exec    # Execute command in service

# Provider auto-detection or explicit
--provider aws|azure|gcp|auto

# Environment management
composey env create prod  # Interactive setup
composey env list
```

**Estimated Effort:** 1-2 weeks

---

#### Gap 3: Live Testing for Azure

**Why Critical:**
- We have AWS live tests, proving the concept works
- Azure needs the same validation
- Critical for production confidence

**What Needs to Be Built:**
```bash
# Bootstrap infrastructure
bootstrap/azure/
- main.tf          # Resource Group, VNet, Container Apps Env
- outputs.tf       # Generate environment.yml

# Smoke test script
scripts/smoke-test-azure.sh

# GitHub Actions workflow
.github/workflows/azure-acceptance.yml

# Azure setup
- Service Principal
- OIDC federation
- GitHub secrets
```

**Estimated Effort:** 1 week (plus Azure subscription setup)

---

#### Gap 4: Smart Inference (Zero Config)

**Current:**
```yaml
services:
  web:
    image: myapp
    x-composey:
      size: medium
      ingress: {}
```

**Desired:**
```yaml
services:
  web:
    image: myapp
    ports:
      - "80:8080"
    # That's it - everything inferred
```

**What Needs to Be Built:**
```python
# Enhanced inference in normalizer.py
def infer_service_config(service):
    # Auto-detect from Dockerfile or image
    if has_port_80_or_8080(service):
        return {"type": "http_service", "public": True}
    
    # Auto-size based on dependencies
    if has_database(service):
        return {"size": "medium"}  # Not small
    
    # Auto-detect scheduled jobs
    if has_cron_in_command(service):
        return {"type": "scheduled_task"}
```

**Estimated Effort:** 1 week

---

### 3.2 Important Gaps (Improve Experience)

#### Gap 5: Runtime Service Discovery

**Current:**
- Connection strings injected at compile time
- Static configuration

**Desired:**
- Services find each other at runtime
- Dynamic updates when services change

**What Needs to Be Built:**
```python
# Options:
1. Consul integration (all clouds)
2. AWS CloudMap (AWS only)
3. Azure Service Discovery (Azure only)
4. Sidecar proxy pattern (Istio/Linkerd)

# Or: Keep compile-time but make it smarter
- Detect service restarts
- Update environment variables
- Rolling updates
```

**Estimated Effort:** 2-3 weeks

---

#### Gap 6: WebSocket Abstraction

**Current:**
- Works on ECS (persistent)
- Limited support elsewhere
- No clear abstraction

**Desired:**
```yaml
services:
  chat:
    image: myapp
    x-composey:
      websocket: true  # Clear intent
```

**What Needs to Be Built:**
```python
# Option 1: Redis Pub/Sub for all clouds
# Option 2: Managed WebSocket service (Pusher/Ably)
# Option 3: Serverless WebSocket (API Gateway, etc.)

# Implementation per cloud:
- AWS: ECS with ElastiCache
- Azure: Container Apps with Redis
- GCP: Cloud Run with Memorystore + max instances
```

**Estimated Effort:** 2 weeks

---

#### Gap 7: Cost Visibility

**Current:**
- No cost information
- User surprised by bills

**Desired:**
```bash
$ composey explain -f compose.yml

Estimated monthly cost:
- web (2-10 instances): $15-75
- db (RDS small): $25
- cache (ElastiCache): $15
- storage (S3): ~$5

Total: ~$60-120/month
(Scales to ~$25 when idle)
```

**What Needs to Be Built:**
```python
# Cost estimation module
- Per-cloud pricing APIs
- Usage pattern estimation
- Cost optimization suggestions
- Alert thresholds
```

**Estimated Effort:** 1-2 weeks

---

### 3.3 Nice-to-Have Gaps (Future)

#### Gap 8: Multi-Region

**What:** Deploy same app to multiple regions

```bash
composey up --provider aws --regions us-east-1,eu-west-1
```

**Complexity:** High

#### Gap 9: Progressive Delivery

**What:** Canary deployments, feature flags

```yaml
x-composey:
  deployment:
    strategy: canary
    steps: [10, 25, 50, 100]
```

**Complexity:** High

#### Gap 10: Observability Integration

**What:** Built-in monitoring, alerting

```yaml
x-composey:
  alerts:
    - metric: error_rate
      threshold: 5%
      action: notify
```

**Complexity:** Medium

---

## 4. Priority Matrix

| Gap | Impact | Effort | Priority | Timeline |
|-----|--------|--------|----------|----------|
| **GCP Support** | High | Medium | 🔴 Critical | 2-3 weeks |
| **Simple CLI** | High | Low | 🔴 Critical | 1-2 weeks |
| **Azure Live Tests** | High | Medium | 🔴 Critical | 1 week |
| **Smart Inference** | High | Low | 🔴 Critical | 1 week |
| **WebSocket Abstraction** | Medium | Medium | 🟡 Important | 2 weeks |
| **Runtime Discovery** | Medium | High | 🟡 Important | 2-3 weeks |
| **Cost Visibility** | Medium | Medium | 🟡 Important | 1-2 weeks |
| **Multi-Region** | Low | High | 🟢 Nice | Future |
| **Progressive Delivery** | Low | High | 🟢 Nice | Future |
| **Observability** | Low | Medium | 🟢 Nice | Future |

---

## 5. Definition of "Done"

### MVP (Minimum Viable Product)

**Must Have:**
- [x] AWS support
- [x] Azure support
- [ ] GCP support
- [ ] Simple CLI (`composey up`)
- [x] Core services (compute, database, cache, storage)
- [ ] Live tests for all providers
- [ ] Documentation

**Timeline**: 4-6 weeks

### Production Ready

**Must Have:**
- [ ] All MVP items
- [ ] Smart inference (zero config for simple cases)
- [ ] WebSocket support
- [ ] Cost estimation
- [ ] Runtime service discovery
- [ ] Comprehensive testing
- [ ] Production runbook

**Timeline**: 8-10 weeks

### Vision Complete

**Must Have:**
- [ ] All production items
- [ ] Multi-region support
- [ ] Progressive delivery
- [ ] Full observability
- [ ] Cost optimization recommendations
- [ ] Enterprise features

**Timeline**: 6 months

---

## 6. Current Position

### We've Built (Foundation) ✅
- Compiler pipeline
- AWS and Azure support
- Auto-scaling
- Managed services (DB, cache, storage)
- Basic testing

### We Need to Build (MVP) 🔧
- GCP support
- Simple CLI
- Azure live tests
- Better inference

### We're ~60% to MVP

**The remaining 40% is:**
1. GCP (validates abstractions)
2. CLI polish (user experience)
3. Testing (production confidence)
4. Documentation (adoption)

---

## 7. Recommendation

### Next 4 Weeks

**Week 1**: GCP Support
- Implement Cloud Run inference
- Implement Cloud SQL inference
- Basic GCP generator

**Week 2**: GCP Support + CLI
- Finish GCP implementation
- Start simple CLI
- GCP golden tests

**Week 3**: CLI + Azure Live Tests
- Finish CLI (`composey up`)
- Azure live test infrastructure
- Azure smoke tests

**Week 4**: Polish + Documentation
- Smart inference improvements
- README updates
- Example applications
- Launch blog post

**Result**: MVP complete, ready for early users

---

## 8. Summary

| Aspect | Status | Gap |
|--------|--------|-----|
| **Core compiler** | ✅ Done | None |
| **AWS support** | ✅ Done | Live tests pass |
| **Azure support** | ✅ Done | Needs live tests |
| **GCP support** | ❌ Missing | Critical gap |
| **CLI experience** | ⚠️ Clunky | Needs simplification |
| **Smart defaults** | ⚠️ Partial | Needs better inference |
| **WebSockets** | ⚠️ Limited | Needs abstraction |
| **Documentation** | ✅ Good | Keep improving |
| **Testing** | ⚠️ Partial | Azure live tests needed |

**Bottom Line**: We're 60% to MVP. The critical gaps are GCP support, CLI polish, and Azure live testing. ~4 weeks of focused work gets us to MVP.
