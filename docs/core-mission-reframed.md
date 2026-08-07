# Composey: Core Mission Reframed

> **Historical vision doc** (pre-dates the Python→Go migration in
> `plan.md`). The mission/philosophy here is still the spirit of the
> project — kept for that reason — but the specific CLI shape shown
> throughout (`composey up --provider aws`) was never implemented.
> The actual two-step CLI is `composey init --provider aws --name prod`
> (one-time platform bootstrap) followed by `composey main -f compose.yml
> -e prod/environment.yml` (per-app deploy) — see `docs/revised-design-env-init.md`
> for the design that was actually built, or `README.md` for current usage.

**The Problem**: Running services locally via Docker Compose is easy. Running them on the cloud is unnecessarily hard.  
**The Mission**: Bridge that gap.

---

## 1. What Makes Docker Compose Easy?

### 1.1 One Command

```bash
docker compose up
```

That's it. Everything just works.

### 1.2 Implicit Networking

```yaml
services:
  web:
    ports:
      - "80:8080"  # Exposed to localhost
    depends_on:
      - db          # Implicit: can reach db

  db:
    image: postgres
    # Implicit: not exposed outside, but web can reach it
```

**No configuration needed for:**
- DNS resolution (`db` resolves to the container)
- Service discovery
- Load balancing
- Security groups
- VPCs

### 1.3 Implicit Persistence

```yaml
services:
  db:
    volumes:
      - db-data:/var/lib/postgresql/data

volumes:
  db-data:  # Just works, persisted between restarts
```

**No configuration needed for:**
- Storage provisioning
- Backup policies
- Snapshot management

### 1.4 Implicit Environment

```yaml
services:
  web:
    environment:
      DATABASE_URL: postgres://db:5432/mydb  # Simple hostname
```

**No configuration needed for:**
- Secret management
- IAM roles
- Service accounts
- Connection strings with tokens/ARNs

### 1.5 Local-First Simplicity

**You think about:**
- Services
- Ports
- Dependencies
- Environment variables

**You DON'T think about:**
- Load balancers
- Subnets
- Security groups
- IAM policies
- Certificate management
- Auto-scaling policies

---

## 2. What Makes Cloud Unnecessarily Hard?

### 2.1 The VPC Problem

```hcl
# Terraform required for basic networking
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_subnet" "public" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.1.0/24"
}

resource "aws_subnet" "private" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.2.0/24"
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
}

resource "aws_nat_gateway" "main" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public.id
}

# ... 20 more resources for basic networking
```

**Why this is hard**: You need to understand networking concepts (CIDR, routing tables, NAT) just to run a simple app.

### 2.2 The Load Balancer Problem

```hcl
# More Terraform for basic HTTP
resource "aws_lb" "main" {
  name               = "main"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.lb.id]
  subnets            = [aws_subnet.public.id]
}

resource "aws_lb_target_group" "main" {
  name     = "main"
  port     = 8080
  protocol = "HTTP"
  vpc_id   = aws_vpc.main.id
  health_check {
    path = "/health"
  }
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"
  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.main.arn
  }
}

# ... 10 more resources
```

**Why this is hard**: Simple `ports: - "80:8080"` becomes 100+ lines of Terraform.

### 2.3 The IAM Problem

```hcl
# IAM for a simple database connection
resource "aws_iam_role" "task" {
  name = "task-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy" "secrets" {
  name = "secrets-policy"
  role = aws_iam_role.task.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = ["secretsmanager:GetSecretValue"]
      Resource = aws_secretsmanager_secret.db.arn
    }]
  })
}
```

**Why this is hard**: You need to understand IAM policies just to read a database password.

### 2.4 The Complexity Explosion

| Docker Compose | AWS Equivalent | Lines of Config |
|---------------|----------------|-----------------|
| `docker compose up` | Terraform + CloudFormation | 500-2000 |
| `ports: - "80:8080"` | ALB + Target Group + Listener + Security Group | 100+ |
| `depends_on: [db]` | VPC + Subnets + Security Groups + IAM | 200+ |
| `volumes: - db-data` | EBS + EFS + Backup Policies | 50+ |
| `environment: [KEY=value]` | Secrets Manager + IAM + Parameter Store | 30+ |

---

## 3. The Bridge: What Composey Should Do

### 3.1 Preserve Docker Compose Simplicity

**Keep the same mental model:**

```yaml
services:
  web:
    image: myapp
    ports:
      - "80:8080"
    depends_on:
      - db
    environment:
      DATABASE_URL: postgres://db:5432/mydb

  db:
    image: postgres:15
    volumes:
      - db-data:/var/lib/postgresql/data

volumes:
  db-data:
```

**No new concepts to learn.**

### 3.2 Hide the Cloud Complexity

**User writes:**
```yaml
ports:
  - "80:8080"
```

**Composey generates:**
- Load balancer (if needed)
- Target group
- Listener rules
- Security groups
- Health checks
- DNS records
- TLS certificates

**But the user never sees this.**

### 3.3 Infer, Don't Configure

**Docker Compose inference:**
- `depends_on` → Service can reach dependency
- `ports` → Exposed to outside
- No `ports` → Internal only

**Composey inference:**
- `depends_on` + `capability: database` → Managed database + connection string
- `ports` → Public endpoint with HTTPS
- No `ports` → Private service discovery
- `image: redis` → Managed cache
- `image: minio` → Managed object storage

### 3.4 Convention Over Configuration

**Docker Compose conventions:**
- Service name = hostname
- `./` paths = bind mounts
- Named volumes = persisted data

**Composey conventions:**
- Service name = stable endpoint
- `capability: database` = managed service (not container)
- `x-composey.size: small` = appropriate instance type
- No explicit networking = sensible defaults

---

## 4. The User Experience

### 4.1 Local Development

```bash
# Exactly the same as today
docker compose up
```

### 4.2 Cloud Deployment

```bash
# One additional command
composey up --provider aws
# or
composey up --provider azure
# or  
composey up --provider gcp
```

**That's it.**

### 4.3 What Happens Under the Hood

```
User runs: composey up --provider aws

1. Parse docker-compose.yml
2. Infer cloud resources needed
3. Generate Terraform
4. Apply Terraform
5. Return endpoint URL

User sees: "Deployed to https://myapp.example.com"
```

### 4.4 What User Doesn't See

- VPC creation
- Subnet configuration
- IAM policy management
- Load balancer setup
- Certificate provisioning
- Security group rules
- Auto-scaling policies

---

## 5. Design Principles

### 5.1 Principle 1: Docker Compose Compatibility

**Valid Docker Compose should just work.**

```yaml
# This should deploy without changes
services:
  web:
    image: nginx
    ports:
      - "80:80"
```

No annotations required for simple cases.

### 5.2 Principle 2: Progressive Enhancement

**Start simple, add complexity only when needed.**

```yaml
# Level 1: Pure Docker Compose (works)
services:
  web:
    image: myapp

# Level 2: Hints for optimization
services:
  web:
    image: myapp
    x-composey:
      size: large
      
# Level 3: Escape hatches
services:
  web:
    image: myapp
    x-composey:
      aws:
        instance_type: m5.xlarge
```

### 5.3 Principle 3: Cloud-Agnostic by Default

**Same compose file works on any cloud.**

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

**Deploys as:**
- AWS: ECS Fargate + RDS + ALB
- Azure: Container Apps + Flexible Server
- GCP: Cloud Run + Cloud SQL

### 5.4 Principle 4: Sensible Defaults

**Make common cases zero-configuration.**

| If user specifies... | Composey assumes... |
|---------------------|---------------------|
| `image: postgres` | Managed database, not container |
| `image: redis` | Managed cache, not container |
| `image: minio` | Managed object storage |
| `ports:` | Public HTTPS endpoint |
| `depends_on:` | Private connectivity |
| No `ports:` | Internal service |

---

## 6. The Abstraction Layer

### 6.1 From Docker Concepts to Cloud Concepts

| Docker Concept | Cloud Concept | User Thinks... |
|---------------|---------------|----------------|
| `services:` | Compute (ECS, Container Apps, Cloud Run) | "My app" |
| `ports:` | Load balancer + HTTPS | "It's accessible" |
| `depends_on:` | Service mesh / service discovery | "They can talk" |
| `volumes:` | Managed storage (EBS, EFS, etc.) | "Data persists" |
| `image: postgres` | Managed database (RDS, etc.) | "I need a database" |
| `environment:` | Secrets + config | "Environment variables" |

### 6.2 The Magic: Inference

```yaml
services:
  web:
    image: myapp
    ports:
      - "80:8080"      # → Needs public endpoint
    depends_on:          # → Needs service discovery
      - db
      - cache

  db:
    image: postgres:15  # → Needs managed database

  cache:
    image: redis:7      # → Needs managed cache
```

**Inference chain:**
1. `web` has ports → Create public endpoint
2. `web` depends on `db` → Create private connectivity
3. `db` is postgres → Create managed database (not container)
4. `cache` is redis → Create managed cache

**Result**: User gets production-grade infrastructure without specifying any of it.

---

## 7. Comparison: Before and After

### 7.1 Before (Pure Terraform)

```hcl
# 500+ lines of Terraform
resource "aws_vpc" "main" { ... }
resource "aws_subnet" "public" { ... }
resource "aws_subnet" "private" { ... }
resource "aws_internet_gateway" "main" { ... }
resource "aws_nat_gateway" "main" { ... }
resource "aws_lb" "main" { ... }
resource "aws_lb_target_group" "main" { ... }
resource "aws_ecs_cluster" "main" { ... }
resource "aws_ecs_task_definition" "web" { ... }
resource "aws_ecs_service" "web" { ... }
resource "aws_rds_instance" "db" { ... }
resource "aws_security_group" "web" { ... }
resource "aws_security_group" "db" { ... }
resource "aws_iam_role" "web" { ... }
# ... and so on
```

### 7.2 After (Composey)

```yaml
# 20 lines of Docker Compose
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

**Both produce the same infrastructure.**

---

## 8. Success Criteria

**Composey is successful when:**

1. ✅ Docker Compose user can deploy to cloud with zero learning curve
2. ✅ Simple apps require zero additional configuration
3. ✅ Complex apps can be optimized with hints (not required)
4. ✅ Same compose file works on any major cloud
5. ✅ User never thinks about VPCs, load balancers, or IAM
6. ✅ Cost is optimized by default (scale-to-zero where possible)

---

## 9. Conclusion

**The vision**: Docker Compose simplicity + Cloud scale

**The method**: Convention over configuration, inference over specification

**The goal**: `docker compose up` → `composey up --provider aws`

**The result**: Cloud deployment becomes as easy as local development.

---

## 10. Next Steps

1. **Simplify the abstraction** - Remove concepts that don't exist in Docker Compose
2. **Improve inference** - Smarter defaults, better image detection
3. **Reduce configuration** - Most examples should work with zero annotations
4. **Documentation** - Position as "Docker Compose for production"
5. **Examples** - Show before/after (500 lines Terraform → 20 lines Compose)
