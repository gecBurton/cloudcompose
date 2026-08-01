# Serverless Debate Reframed: Docker Compose Simplicity

**Core Mission**: `docker compose up` → `composey up --provider aws|azure|gcp`

**Question**: Does serverless help or hinder this mission?

---

## 1. What Does Docker Compose Actually Do?

### Local Experience

```bash
docker compose up
```

**What happens:**
1. Builds images if needed
2. Creates a network (implicit)
3. Starts containers
4. Wires up dependencies
5. Exposes ports to localhost

**User mental model:**
> "I have a web service and a database. They can talk to each other."

**User does NOT think:**
- "What's the VPC CIDR block?"
- "Do I need a NAT gateway?"
- "What's my IAM policy?"
- "Should I use Fargate or Lambda?"

---

## 2. The Serverless Question Reframed

### Original Question
> "Should we optimize for serverless (scale-to-zero) or traditional (always-on)?"

### Better Question
> "What infrastructure gets out of the user's way and just runs their services?"

---

## 3. What Users Actually Care About

### User Survey (Hypothetical)

**Question**: "When you run `docker compose up`, what do you care about?"

| Concern | Priority | Notes |
|---------|----------|-------|
| "It just works" | 🔴 Critical | Same as local |
| "It's cheap when idle" | 🟡 Important | But not at complexity cost |
| "It scales under load" | 🟡 Important | But most apps don't need massive scale |
| "It uses serverless" | 🟢 Nice-to-have | Implementation detail |
| "It uses containers" | 🟢 Nice-to-have | Implementation detail |

### The Insight

**Users don't care about serverless vs containers.**

They care about:
1. ✅ Simplicity (same as `docker compose up`)
2. ✅ Cost (don't pay for idle)
3. ✅ Reliability (works in production)
4. ❌ NOT: "Is it serverless?"

---

## 4. What Blocks Simplicity?

### Complexity Sources

| Source | Problem | Serverless Helps? |
|--------|---------|-------------------|
| **VPC networking** | Subnets, routing, NAT | Partially (managed, but still complex) |
| **Load balancers** | ALB configuration, SSL | Yes (built-in) |
| **Auto-scaling** | Target tracking, policies | Yes (automatic) |
| **Service discovery** | DNS, CloudMap | Mixed (different per cloud) |
| **State management** | Volumes, databases | No (same complexity) |
| **WebSockets** | Connection persistence | No (harder on serverless) |

### Key Insight

**Serverless removes SOME complexity but adds OTHER complexity.**

**Removes:**
- Server management
- Capacity planning
- Load balancer configuration

**Adds:**
- Request timeouts (hard limits)
- Cold starts (architectural concern)
- State externalization (required)
- Connection limits (concurrency caps)

---

## 5. The Simplicity Test

### Test Case: Simple Web App + Database

```yaml
services:
  web:
    image: myapp
    ports:
      - "80:8080"
    depends_on:
      - db

  db:
    image: postgres:15
```

### Option A: ECS Fargate (Traditional)

**User experience:**
```bash
composey up --provider aws
```

**What gets created:**
- VPC + Subnets
- ALB + Target Group
- ECS Cluster + Service
- RDS PostgreSQL
- Security Groups
- IAM Roles

**Complexity:** High (many resources)

**But:**
- ✅ WebSockets just work
- ✅ No request timeouts
- ✅ Simple mental model ("my app is running")

### Option B: Lambda + API Gateway (Serverless)

**User experience:**
```bash
composey up --provider aws
```

**What gets created:**
- Lambda functions
- API Gateway
- RDS Proxy (for database connections)
- RDS PostgreSQL
- IAM Roles
- VPC (for Lambda to reach RDS)

**Complexity:** Very High (RDS Proxy required!)

**And:**
- ❌ Request timeouts (29s limit)
- ❌ Cold starts
- ❌ Complex mental model ("my function is invoked")

### Option C: Container Apps / Cloud Run (Serverless Containers)

**User experience:**
```bash
composey up --provider aws
```

**What gets created:**
- Container Apps Environment
- Container App
- Flexible Server PostgreSQL
- VNet integration

**Complexity:** Medium

**And:**
- ⚠️ Request timeouts (configurable, but exist)
- ⚠️ Cold starts (faster, but exist)
- ✅ Simpler mental model ("my container is running")

---

## 6. The Verdict: What Maximizes Simplicity?

### For Different Use Cases

| Use Case | Best Fit | Why |
|----------|----------|-----|
| **Simple web app** | Container Apps / Cloud Run | Simple, cheap, fast enough |
| **API backend** | Container Apps / Cloud Run | Scale-to-zero saves money |
| **Background worker** | Lambda (small) / Container Apps | Event-driven is natural |
| **WebSocket app** | ECS / GKE Autopilot | Connections need persistence |
| **Long-running job** | ECS / Container Apps Jobs | No timeout pressure |
| **Microservices** | Container Apps / Cloud Run | Service discovery built-in |

### The Pattern

**"Serverless containers" (Container Apps, Cloud Run) hit the sweet spot:**

- ✅ Same mental model as Docker Compose (containers)
- ✅ Scale-to-zero (cost optimization)
- ✅ Built-in load balancing (simplicity)
- ✅ Faster cold starts than Lambda
- ✅ No VPC complexity on Azure
- ⚠️ Some timeout limits (usually acceptable)

**ECS Fargate is backup for:**
- WebSockets
- Long-running processes
- Complex networking

**Lambda is for:**
- True functions (< 15 min)
- Event processing
- Not general services

---

## 7. Implications for Composey

### The New Design

```yaml
services:
  # Default: Serverless containers
  web:
    image: myapp
    ports:
      - "80:8080"
    depends_on:
      - db
    # Implicit: type: container_service

  # Escape hatch: Traditional
  websocket:
    image: myapp
    x-composey:
      type: persistent_service  # ECS, VMs, etc.

  # Escape hatch: Functions
  processor:
    image: myapp
    x-composey:
      type: function  # Lambda, etc.

db:
  image: postgres:15
  # Implicit: managed database (not container)
```

### Implementation Per Cloud

| Cloud | Default | WebSockets | Functions |
|-------|---------|------------|-----------|
| **AWS** | ECS Fargate (serverless) | ECS Fargate (always-on) | Lambda |
| **Azure** | Container Apps | Container Apps (min replicas) | Container Apps Jobs |
| **GCP** | Cloud Run | Cloud Run (min instances) | Cloud Functions |

### Key Decision

**Default to "serverless containers" because:**

1. ✅ Matches Docker Compose mental model (containers)
2. ✅ Simpler than traditional (no ALB, no capacity planning)
3. ✅ Cheaper than traditional (scale-to-zero)
4. ✅ Works for 80% of use cases
5. ✅ Easy escape hatches for the 20%

**NOT because "serverless is the future"**

**But because "serverless containers are simpler for most apps"**

---

## 8. What We Tell Users

### Messaging

> "Composey runs your Docker Compose services in the cloud.  
> By default, we use serverless containers that scale to zero (saving you money).  
> If you need persistent connections (WebSockets) or long-running processes, we have escape hatches."

### Not This

> "We're a serverless platform."  
> (Too narrow, excludes use cases)

### Not This Either

> "We deploy Docker Compose to ECS."  
> (Too specific, forces always-on containers)

### But This

> "We run your Docker Compose services optimized for each cloud.  
> Serverless by default. Persistent when needed."

---

## 9. Conclusion

### The Serverless Debate Resolved

**Question**: Should we use serverless?

**Answer**: Use whatever is simplest for the user.

**For 80% of apps**: Serverless containers (Container Apps, Cloud Run)
- Simpler than traditional
- Cheaper than traditional
- Matches Docker Compose mental model

**For 20% of apps**: Traditional containers (ECS, VMs)
- WebSockets need persistence
- Long-running processes
- Complex networking

### The Core Principle

> **Docker Compose simplicity > Serverless purity**

We choose serverless containers NOT because they're serverless,
but because they're SIMPLER for the user's mental model.

---

## 10. Final Architecture

```yaml
# User writes (Docker Compose)
services:
  web:
    image: myapp
    ports:
      - "80:8080"
    depends_on:
      - db

  db:
    image: postgres:15

# Composey deploys:
# - Serverless containers (default)
# - Managed database (detected)
# - Built-in load balancing
# - Service discovery
# - Scale-to-zero

# Result: Simple, cheap, production-ready
```

**The goal**: `docker compose up` → `composey up`

**The method**: Serverless containers by default, escape hatches when needed.

**The why**: Simplicity, not serverless for its own sake.
