# AWS Simplification Plan

Based on cross-cloud analysis showing AWS requires 7x more resources than GCP.

## Current State (The Problem)

### AWS: 43 Resources for Simple Flask App

```
Compute:        4 resources (ECS Service + Task Definition + Log Group + Auto-scaling)
Database:       1 resource  (RDS)
Networking:    10 resources (VPC, Subnets, Security Groups, Service Discovery, etc.)
IAM:           10 resources (Roles, Policies)
Load Balancer:  4 resources (ALB, Target Group, Listener, Certificate)
Secrets:        3 resources (Secrets Manager)
Other:         11 resources (Various helpers)
---
Total:         43 resources
```

### GCP: 6 Resources for Same App

```
Compute:        1 resource  (Cloud Run Service)
Database:       1 resource  (Cloud SQL)
Networking:     1 resource  (VPC Connector - only for private DB)
---
Total:          6 resources
```

**The Gap**: AWS has 37 more resources than necessary!

---

## Root Causes

### 1. Explicit Load Balancer (8 resources)

**Current:**
```hcl
aws_lb                    # Load balancer
aws_lb_target_group       # Target group
aws_lb_listener           # Listener
aws_lb_listener_rule      # Routing rules
aws_security_group        # LB security group
aws_security_group_rule   # Ingress rules
data aws_lb               # Data source
data aws_lb_listener      # Data source
```

**Problem**: User must provide ALB ARN, understand load balancing concepts.

### 2. IAM Complexity (10 resources)

**Current:**
```hcl
aws_iam_role              # Task role
aws_iam_role              # Execution role
aws_iam_role_policy       # Task policy
aws_iam_role_policy       # Execution policy (ECR)
aws_iam_role_policy       # Secrets access
aws_iam_role_policy       # CloudWatch logs
... (4 more for each service)
```

**Problem**: Every service needs explicit IAM setup.

### 3. Networking Sprawl (10 resources)

**Current:**
```hcl
aws_security_group        # Service security group
aws_security_group_rule   # Ingress rules (xN)
aws_security_group_rule   # Egress rules
aws_db_subnet_group       # DB subnet group
aws_service_discovery_private_dns_namespace  # DNS namespace
aws_service_discovery_service                # DNS service
... (4 more)
```

**Problem**: VPC, subnets, security groups all explicit.

### 4. ECS Split (2 resources vs 1)

**Current:**
```hcl
aws_ecs_task_definition   # Container spec
aws_ecs_service           # Service orchestration
```

**Problem**: Two resources where one would suffice.

---

## The Plan: 5 Phase Approach

### Phase 1: Auto-Create VPC (Week 1)

**Goal**: Remove VPC from required environment config

**Current Required:**
```yaml
vpc_id: vpc-123
public_subnets: [subnet-1, subnet-2]
private_subnets: [subnet-3, subnet-4]
```

**Desired:**
```yaml
# Nothing! Auto-created per application
```

**Implementation:**
```hcl
# New: Auto-create VPC if not provided
resource "aws_vpc" "app" {
  cidr_block = "10.0.0.0/16"
  
  tags = {
    Name = "${var.app_name}-vpc"
  }
}

resource "aws_subnet" "public" {
  count = 2
  vpc_id = aws_vpc.app.id
  cidr_block = "10.0.${count.index + 1}.0/24"
  availability_zone = data.aws_availability_zones.available.names[count.index]
  map_public_ip_on_launch = true
}

resource "aws_subnet" "private" {
  count = 2
  vpc_id = aws_vpc.app.id
  cidr_block = "10.0.${count.index + 10}.0/24"
  availability_zone = data.aws_availability_zones.available.names[count.index]
}

resource "aws_internet_gateway" "app" {
  vpc_id = aws_vpc.app.id
}

resource "aws_nat_gateway" "app" {
  allocation_id = aws_eip.nat.id
  subnet_id = aws_subnet.public[0].id
}
```

**Impact**: -4 resources from environment config

**Configuration:**
```yaml
# Option 1: Bring your own VPC (existing)
vpc_id: vpc-123

# Option 2: Auto-create (new default)
# (nothing - created automatically)
```

---

### Phase 2: Auto-Create ALB (Week 1)

**Goal**: Remove ALB from required environment config

**Current Required:**
```yaml
alb_arn: arn:aws:elasticloadbalancing:...
alb_listener_arn: arn:aws:elasticloadbalancing:...
alb_security_group_id: sg-...
```

**Desired:**
```yaml
# Nothing! Created when first public service deployed
```

**Implementation:**
```hcl
# New: Auto-create ALB for first public service
resource "aws_lb" "app" {
  count = length(local.public_services) > 0 ? 1 : 0
  
  name = "${var.app_name}-alb"
  internal = false
  load_balancer_type = "application"
  security_groups = [aws_security_group.alb[0].id]
  subnets = aws_subnet.public[*].id
  
  enable_deletion_protection = var.retain_data_on_destroy
}

resource "aws_security_group" "alb" {
  count = length(local.public_services) > 0 ? 1 : 0
  
  name = "${var.app_name}-alb-sg"
  vpc_id = local.vpc_id
  
  ingress {
    from_port = 80
    to_port = 80
    protocol = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  
  ingress {
    from_port = 443
    to_port = 443
    protocol = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
}
```

**Impact**: -4 resources from environment config

---

### Phase 3: Simplified IAM (Week 2)

**Goal**: Auto-generate IAM with sensible defaults

**Current:**
```hcl
# 10 resources per service
aws_iam_role.task_role
aws_iam_role.execution_role
aws_iam_role_policy.task_policy
aws_iam_role_policy.execution_ecr
aws_iam_role_policy.execution_logs
aws_iam_role_policy.secrets_access
...
```

**Desired:**
```hcl
# Single module call
module "service_iam" {
  source = "./modules/iam-service"
  
  service_name = "web"
  needs_database = true
  needs_cache = true
  needs_secrets = true
}
```

**Implementation:**

Create `composey/compiler/inference/aws/_iam.py`:
```python
def create_service_iam(resources, service_name, needs_db=False, needs_cache=False):
    """Create all IAM resources for a service with sensible defaults."""
    
    # Single role for both task and execution
    role_key = f"{service_name}_role"
    resources.aws_iam_role[role_key] = IamRole(
        name=f"{service_name}-role",
        assume_role_policy=json.dumps({
            "Version": "2012-10-17",
            "Statement": [{
                "Action": "sts:AssumeRole",
                "Effect": "Allow",
                "Principal": {"Service": "ecs-tasks.amazonaws.com"}
            }]
        }),
    )
    
    # Single policy with all needed permissions
    policy_key = f"{service_name}_policy"
    statements = [
        # ECR access
        {
            "Effect": "Allow",
            "Action": [
                "ecr:GetAuthorizationToken",
                "ecr:BatchCheckLayerAvailability",
                "ecr:GetDownloadUrlForLayer",
                "ecr:BatchGetImage",
            ],
            "Resource": "*"
        },
        # CloudWatch logs
        {
            "Effect": "Allow",
            "Action": [
                "logs:CreateLogStream",
                "logs:PutLogEvents",
            ],
            "Resource": "*"
        },
    ]
    
    if needs_db:
        statements.append({
            "Effect": "Allow",
            "Action": ["secretsmanager:GetSecretValue"],
            "Resource": f"${{aws_secretsmanager_secret.{service_name}_db.arn}}"
        })
    
    resources.aws_iam_role_policy[policy_key] = IamRolePolicy(
        name=f"{service_name}-policy",
        role=f"${{aws_iam_role.{role_key}.name}}",
        policy=json.dumps({"Version": "2012-10-17", "Statement": statements}),
    )
```

**Impact**: 10 IAM resources → 2 resources per service

---

### Phase 4: Unified Security Groups (Week 2)

**Goal**: Simplify networking with intelligent defaults

**Current:**
```hcl
aws_security_group.web                    # Service SG
aws_security_group_rule.web_ingress_80    # Port 80
aws_security_group_rule.web_ingress_443   # Port 443
aws_security_group_rule.web_egress_all    # Egress
aws_security_group.db                     # DB SG
aws_security_group_rule.db_ingress_5432   # Postgres port
```

**Desired:**
```hcl
# Single security group per service
aws_security_group.web  # With rules inline
aws_security_group.db   # With rules inline
```

**Implementation:**
```python
def create_security_group(resources, service, ingress_ports, egress_cidr):
    """Create SG with inline rules instead of separate resources."""
    
    # Inline ingress rules (no separate aws_security_group_rule resources)
    ingress_rules = []
    for port in ingress_ports:
        ingress_rules.append({
            "from_port": port,
            "to_port": port,
            "protocol": "tcp",
            "cidr_blocks": ["0.0.0.0/0"] if service.ingress else [local.vpc_cidr],
        })
    
    resources.aws_security_group[service.name] = SecurityGroup(
        name=f"{service.name}-sg",
        vpc_id=local.vpc_id,
        ingress=ingress_rules,  # Inline!
        egress=[{
            "from_port": 0,
            "to_port": 0,
            "protocol": "-1",
            "cidr_blocks": [egress_cidr],
        }],
    )
```

**Impact**: 4 SG resources → 1 per service

---

### Phase 5: Optional Service Discovery (Week 3)

**Goal**: Only create service discovery when needed

**Current:**
```hcl
aws_service_discovery_private_dns_namespace  # Always created
aws_service_discovery_service                # One per service
```

**Desired:**
```hcl
# Only create when services need to talk to each other
# Use compile-time injection where possible
```

**Implementation:**
```python
def needs_service_discovery(app):
    """Check if runtime service discovery is actually needed."""
    # If all connections can be resolved at compile time,
    # we don't need CloudMap
    
    for service in app.services:
        if service.capability == "container":
            # Check if any dependency needs runtime discovery
            for rel in app.relationships:
                if rel.client == service.name:
                    target = next((s for s in app.services if s.name == rel.server), None)
                    if target and target.capability == "container":
                        # Container → Container needs discovery
                        return True
    
    return False
```

**Impact**: Remove 2 resources when not needed

---

## Implementation Order

### Week 1: Infrastructure Auto-Creation

**Day 1-2**: VPC Auto-Creation
- Modify `AwsEnvironment` to make VPC optional
- Create VPC module
- Update networking inference

**Day 3-4**: ALB Auto-Creation
- Make ALB optional in environment
- Create ALB automatically for first public service
- Handle certificate management

**Day 5**: Testing & Refinement
- Golden file updates
- Integration tests

### Week 2: IAM & Networking Simplification

**Day 1-2**: IAM Module
- Create `_iam.py` module
- Auto-generate policies
- Reduce IAM resource count

**Day 3-4**: Security Group Consolidation
- Inline rules in SG resource
- Reduce networking resource count

**Day 5**: Testing & Refinement
- Golden file updates
- Security testing

### Week 3: Optimization & Polish

**Day 1-2**: Optional Service Discovery
- Only create when needed
- Use compile-time injection

**Day 3-4**: Documentation
- Update README
- Migration guide

**Day 5**: Final Testing
- Full test suite
- Performance testing
- Documentation review

---

## Expected Results

### Before Simplification

```
Total Resources: 43
- Environment config: 12 fields required
- User must understand: VPC, ALB, IAM, Security Groups
- Time to first deploy: 2-3 hours (learning curve)
```

### After Simplification

```
Total Resources: ~15 (internal, hidden from user)
- Environment config: 2 fields required (region, optional vpc_id)
- User understands: Services, Ports, Dependencies (like Docker Compose)
- Time to first deploy: 10 minutes
```

### Comparison with GCP

| Metric | AWS (Before) | AWS (After) | GCP |
|--------|--------------|-------------|-----|
| Resources | 43 | ~15 | 6 |
| Config Fields | 12 | 2 | 2 |
| User Concepts | 8 | 3 | 3 |
| Deploy Time | 2-3 hours | 10 min | 10 min |

**Goal**: AWS feels as simple as GCP to the user!

---

## Risk Mitigation

### Risk: Backward Compatibility

**Mitigation**:
- Keep VPC/ALB optional, not removed
- Existing configs still work
- Deprecation warnings before removal

### Risk: Cost (Auto-created VPC)

**Mitigation**:
- NAT Gateway is the expensive part (~$32/month)
- Option to use "public subnets only" mode
- Document cost implications

### Risk: Complexity in Code

**Mitigation**:
- Well-tested modules
- Clear separation of concerns
- Comprehensive documentation

---

## Success Metrics

### Quantitative

- [ ] AWS resource count < 20 (from 43)
- [ ] Environment config fields < 5 (from 12)
- [ ] Time to first deploy < 15 minutes
- [ ] All 315 tests still pass

### Qualitative

- [ ] User feedback: "As easy as GCP"
- [ ] No VPC knowledge required
- [ ] No ALB knowledge required
- [ ] No IAM knowledge required

---

## Conclusion

This plan transforms AWS from the most complex cloud to one that's as simple as GCP, while maintaining all functionality.

**Key Insight**: The complexity isn't in the concepts (they're the same across clouds), it's in AWS's explicit resource model. By auto-creating sensible defaults, we hide that complexity.

**Timeline**: 3 weeks of focused work
**Outcome**: AWS parity with GCP in terms of user experience
