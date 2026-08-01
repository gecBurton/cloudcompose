# Azure Live Testing Implementation Plan

**Status**: Proposed  
**Priority**: High  
**Estimated Effort**: 4-6 days  
**Owner**: TBD

---

## 1. Executive Summary

### Current State
- **AWS**: Full live testing with automated deployment, verification, and teardown
- **Azure**: Golden file tests only (validates Terraform output, not actual deployment)

### Goal
Implement Azure live testing to achieve parity with AWS testing infrastructure.

### Success Criteria
- [ ] Automated deployment to real Azure
- [ ] Smoke tests against live Container Apps
- [ ] Managed resource verification (PostgreSQL, Redis, Storage)
- [ ] CI/CD integration with GitHub Actions
- [ ] Cost tracking and cleanup guarantees

---

## 2. Architecture

### 2.1 AWS Reference Architecture

```
Bootstrap (1x per test run)
├── VPC
├── ECS Cluster
├── Application Load Balancer
└── Subnets / NAT Gateway

App Deployment (1x per example)
├── ECS Service + Task Definition
├── RDS / ElastiCache / S3 (if needed)
└── ALB Listener Rules

Verification
├── Poll ALB DNS endpoint
├── Assert response contains expected content
└── Verify managed resources in AWS API
```

### 2.2 Azure Target Architecture

```
Bootstrap (1x per test run)
├── Resource Group
├── Container Apps Environment
├── Virtual Network + Subnet
└── Log Analytics Workspace

App Deployment (1x per example)
├── Container App
├── PostgreSQL/MySQL Flexible Server (if needed)
├── Redis Cache (if needed)
└── Storage Account (if needed)

Verification
├── Poll Container App FQDN
├── Assert response contains expected content
└── Verify managed resources in Azure API
```

### 2.3 Key Differences

| Aspect | AWS | Azure |
|--------|-----|-------|
| Entry Point | ALB DNS Name | Container App FQDN |
| Load Balancer | Separate ALB resource | Built into Container Apps |
| Bootstrap Time | ~5 minutes | ~3 minutes |
| Cold Start | ECS Fargate (~30s) | Container Apps (~10s) |
| State Backend | S3 | Azure Blob Storage |
| Authentication | OIDC → AWS IAM | OIDC → Azure Service Principal |

---

## 3. Implementation Phases

### Phase 1: Bootstrap Infrastructure (Days 1-2)

#### 3.1.1 Create `bootstrap/azure/` Directory

**Files to create:**
- `bootstrap/azure/main.tf` - Core infrastructure
- `bootstrap/azure/variables.tf` - Input variables
- `bootstrap/azure/outputs.tf` - Environment outputs
- `bootstrap/azure/environment.yml.tpl` - Template for composey environment

**Resources to provision:**
```hcl
resource "azurerm_resource_group" "main" {
  name     = var.name
  location = var.location
}

resource "azurerm_log_analytics_workspace" "main" {
  name                = "${var.name}-logs"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  sku                 = "PerGB2018"
  retention_in_days   = 7
}

resource "azurerm_virtual_network" "main" {
  name                = "${var.name}-vnet"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  address_space       = ["10.0.0.0/16"]
}

resource "azurerm_subnet" "infrastructure" {
  name                 = "infrastructure"
  resource_group_name  = azurerm_resource_group.main.name
  virtual_network_name = azurerm_virtual_network.main.name
  address_prefixes     = ["10.0.0.0/21"]
  
  delegation {
    name = "container-apps"
    service_delegation {
      name    = "Microsoft.App/environments"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

resource "azurerm_container_app_environment" "main" {
  name                       = "${var.name}-env"
  location                   = azurerm_resource_group.main.location
  resource_group_name        = azurerm_resource_group.main.name
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id
  infrastructure_subnet_id   = azurerm_subnet.infrastructure.id
}

resource "azurerm_container_registry" "main" {
  name                = "${var.name}acr"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  sku                 = "Standard"
  admin_enabled       = true
}
```

**Outputs:**
- `environment.yml` - For composey compilation
- `container_app_environment_id` - Reference for apps
- `acr_login_server` - For image pushes

#### 3.1.2 Acceptance Criteria
- [ ] `terraform apply` successfully creates all resources
- [ ] `terraform output` produces valid environment.yml
- [ ] Resources can be destroyed cleanly
- [ ] Cost tracking tags applied

---

### Phase 2: Smoke Test Script (Day 3)

#### 3.2.1 Create `scripts/smoke-test-azure.sh`

**Structure (adapted from AWS script):**

```bash
#!/usr/bin/env bash
set -euo pipefail

# Configuration
NAME="${NAME:-smoke}"
LOCATION="${LOCATION:-eastus}"
COMPOSE="${COMPOSE:-examples/hello/compose.yml}"
PROJECT="${PROJECT:-hello}"
EXPECT="${EXPECT:-Server name}"
POLL_TIMEOUT="${POLL_TIMEOUT:-300}"
KEEP="${KEEP:-0}"

# Paths
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BOOTSTRAP_DIR="$ROOT/bootstrap/azure"
BUILD_DIR="$ROOT/build/$PROJECT"

# Functions
log() { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }
fail() { printf '\n\033[1;31mFAIL:\033[0m %s\n' "$*" >&2; exit 1; }

# Teardown trap
cleanup() {
  [[ "$KEEP" == "1" ]] && exit
  # Destroy app first, then bootstrap
  (cd "$BUILD_DIR" && terraform destroy -auto-approve)
  (cd "$BOOTSTRAP_DIR" && terraform destroy -auto-approve)
}
trap cleanup EXIT INT TERM

# 1. Deploy bootstrap
log "Deploying Azure bootstrap environment..."
cd "$BOOTSTRAP_DIR"
terraform init -input=false
terraform apply -auto-approve -var="name=$NAME" -var="location=$LOCATION"

# 2. Generate environment.yml
terraform output -raw environment_yaml > "$BOOTSTRAP_DIR/environment.yml"

# 3. Compile with composey
log "Compiling $COMPOSE..."
uv run composey -f "$COMPOSE" -e "$BOOTSTRAP_DIR/environment.yml" \
  -p "$PROJECT" -o "$BUILD_DIR"

# 4. Deploy app
log "Deploying app..."
cd "$BUILD_DIR"
terraform init -input=false
terraform apply -auto-approve

# 5. Get Container App FQDN
FQDN="$(terraform output -raw container_app_fqdn)"
[[ -n "$FQDN" ]] || fail "No FQDN output"

# 6. Poll until healthy
url="https://$FQDN"
log "Polling $url..."
# ... poll loop similar to AWS ...

log "SUCCESS - Azure live test passed!"
```

#### 3.2.2 Key Differences from AWS Script

| Aspect | AWS | Azure |
|--------|-----|-------|
| Entry point | `alb_dns_name` | `container_app_fqdn` |
| Protocol | HTTP | HTTPS (Container Apps default) |
| State backend | S3 | Azure Blob Storage (optional) |
| Auth method | AWS CLI / OIDC | Azure CLI / OIDC |

#### 3.2.3 Acceptance Criteria
- [ ] Script runs end-to-end with `hello` example
- [ ] Successfully deploys bootstrap
- [ ] Successfully compiles and deploys app
- [ ] Polls FQDN and verifies response
- [ ] Cleans up resources on exit
- [ ] Handles errors gracefully

---

### Phase 3: CI/CD Integration (Day 4)

#### 3.3.1 Create `.github/workflows/azure-acceptance.yml`

```yaml
name: Azure Acceptance

on:
  workflow_dispatch:
    inputs:
      example:
        description: Example to smoke test
        type: choice
        default: hello
        options:
          - hello
          - flask
          - flask-redis
          - flask-s3
          - nginx-flask-mysql

permissions:
  id-token: write
  contents: read

concurrency:
  group: azure-acceptance
  cancel-in-progress: false

jobs:
  smoke:
    runs-on: ubuntu-latest
    timeout-minutes: 90
    steps:
      - uses: actions/checkout@v7

      - name: Azure Login (OIDC)
        uses: azure/login@v2
        with:
          client-id: ${{ vars.AZURE_CLIENT_ID }}
          tenant-id: ${{ vars.AZURE_TENANT_ID }}
          subscription-id: ${{ vars.AZURE_SUBSCRIPTION_ID }}

      - name: Setup Terraform
        uses: hashicorp/setup-terraform@v4
        with:
          terraform_version: "1.10.0"
          terraform_wrapper: false

      - name: Setup uv
        uses: astral-sh/setup-uv@v7
        with:
          python-version: "3.14"

      - name: Smoke test
        env:
          NAME: ci${{ github.run_number }}
          LOCATION: eastus
        run: |
          case "${{ inputs.example }}" in
            hello)
              export COMPOSE=examples/hello/compose.yml PROJECT=hello \
                     EXPECT="Hello from Azure" ;;
            flask)
              export COMPOSE=examples/flask/compose.yml PROJECT=flask \
                     EXPECT="Hello World" ;;
            # ... more examples ...
          esac
          scripts/smoke-test-azure.sh
```

#### 3.3.2 Azure Service Principal Setup

**Prerequisites:**
1. Azure subscription with owner/contributor access
2. Azure AD permissions to create service principals

**Setup Steps:**

```bash
# 1. Create service principal
az ad sp create-for-rbac \
  --name "composey-acceptance" \
  --role "Contributor" \
  --scopes /subscriptions/{SUBSCRIPTION_ID}

# 2. Create federated credentials for GitHub OIDC
# Go to Azure Portal > Azure AD > App Registrations > composey-acceptance
# Add federated credential:
# - Organization: gecBurton
# - Repository: composey
# - Entity type: Environment / Branch / Pull request

# 3. Configure resource group for acceptance tests
az group create \
  --name "composey-acceptance" \
  --location eastus
```

#### 3.3.3 GitHub Secrets/Variables

**Repository Variables:**
```
AZURE_CLIENT_ID       # Service principal application (client) ID
AZURE_TENANT_ID       # Azure AD tenant ID
AZURE_SUBSCRIPTION_ID # Azure subscription ID
```

**Repository Secrets:**
```
# None required for OIDC - authentication is via federated credentials
```

#### 3.3.4 Acceptance Criteria
- [ ] Workflow appears in GitHub Actions
- [ ] Manual trigger works
- [ ] OIDC authentication succeeds
- [ ] Bootstrap deploys successfully
- [ ] App compiles and deploys
- [ ] Test passes and cleans up

---

### Phase 4: Managed Resource Assertions (Days 5-6)

#### 3.4.1 Create `scripts/assert_managed_azure.py`

**Purpose:** Verify that managed services (PostgreSQL, Redis, Storage) were actually created

**Implementation:**

```python
#!/usr/bin/env python3
"""Assert that composey-managed Azure resources exist."""

import json
import sys
from azure.identity import DefaultAzureCredential
from azure.mgmt.rdbms.postgresql_flexibleservers import PostgreSQLManagementClient
from azure.mgmt.redis import RedisManagementClient
from azure.mgmt.storage import StorageManagementClient

def main():
    # Read Terraform state from stdin
    state = json.load(sys.stdin)
    
    # Extract resource group from state
    resources = state.get('values', {}).get('root_module', {}).get('resources', [])
    
    credential = DefaultAzureCredential()
    
    # Check for PostgreSQL
    postgresql_servers = [r for r in resources if r['type'] == 'azurerm_postgresql_flexible_server']
    if postgresql_servers:
        client = PostgreSQLManagementClient(credential, subscription_id)
        for server in postgresql_servers:
            rg = server['values']['resource_group_name']
            name = server['values']['name']
            # Verify server exists in Azure API
            try:
                client.servers.get(rg, name)
                print(f"✓ PostgreSQL server {name} exists")
            except Exception as e:
                print(f"✗ PostgreSQL server {name} not found: {e}")
                sys.exit(1)
    
    # Similar checks for Redis, Storage...
    
    print("All managed resources verified!")

if __name__ == "__main__":
    main()
```

#### 3.4.2 Integration with Smoke Test

Add to `smoke-test-azure.sh`:

```bash
# After successful health check
log "Verifying managed resources..."
(cd "$BUILD_DIR" && terraform show -json) | python3 "$ROOT/scripts/assert_managed_azure.py" \
  || fail "Managed resource assertions failed"
```

#### 3.4.3 Acceptance Criteria
- [ ] Script detects PostgreSQL servers in TF state
- [ ] Script verifies servers exist in Azure API
- [ ] Script detects Redis caches
- [ ] Script verifies caches exist
- [ ] Script detects Storage accounts
- [ ] Script verifies accounts exist
- [ ] Script exits non-zero on missing resources

---

## 4. Cost Management

### 4.1 Estimated Costs Per Test Run

| Resource | Azure Cost | Duration | Total |
|----------|-----------|----------|-------|
| Container Apps Environment | ~$0.50/day | 0.5 hours | ~$0.01 |
| Container App (small) | ~$0.000024/vCPU/s | 0.5 hours | ~$0.05 |
| PostgreSQL Flexible (B1ms) | ~$0.02/hour | 0.5 hours | ~$0.01 |
| Redis Cache (C1) | ~$0.02/hour | 0.5 hours | ~$0.01 |
| Storage Account | ~$0.02/day | 0.5 hours | ~$0.01 |
| **Total** | | | **~$0.10** |

With bootstrap resources (NAT, VNet): **~$2.00 per test run**

### 4.2 Cost Controls

1. **Time-bounded**: 90 minute timeout
2. **Automatic cleanup**: Always destroy on exit (unless KEEP=1)
3. **Resource limits**: Use smallest SKUs (Burstable, Basic)
4. **Scheduling**: Run on-demand only (not on every PR)
5. **Resource group tagging**: Track costs by run ID

---

## 5. Risk Mitigation

### 5.1 Cleanup Failures

**Risk**: Resources leak if cleanup fails

**Mitigation:**
- Remote state storage (Azure Blob) for recovery
- Resource group-level cleanup scripts
- Weekly cleanup automation
- Cost alerts on subscription

### 5.2 Concurrent Runs

**Risk**: Multiple runs conflict

**Mitigation:**
- GitHub Actions concurrency group
- Unique naming per run (`ci${{ github.run_number }}`)
- Queue instead of cancel

### 5.3 Authentication Issues

**Risk**: OIDC or SP credentials expire/invalid

**Mitigation:**
- Separate SP for acceptance tests
- Credential rotation alerts
- Fallback to manual testing

---

## 6. Success Metrics

### 6.1 Implementation Complete When

- [ ] Bootstrap deploys successfully
- [ ] `hello` example passes smoke test
- [ ] All 5 examples pass smoke test
- [ ] GitHub Actions workflow runs
- [ ] Managed resource assertions pass
- [ ] Documentation complete
- [ ] Cost monitoring in place

### 6.2 Ongoing Metrics

| Metric | Target | Measurement |
|--------|--------|-------------|
| Test success rate | >95% | GitHub Actions runs |
| Average runtime | <20 min | GitHub Actions logs |
| Cost per run | <$2.50 | Azure cost analysis |
| Resource leaks | 0 | Weekly cleanup reports |

---

## 7. Dependencies

### 7.1 External Dependencies

- [ ] Azure subscription with sufficient quota
- [ ] Azure AD permissions for SP creation
- [ ] GitHub repository permissions for secrets/variables

### 7.2 Internal Dependencies

- [ ] Azure provider implementation complete ✅
- [ ] Golden file tests passing ✅
- [ ] Bootstrap Terraform reviewed

---

## 8. Timeline

| Phase | Days | Owner | Status |
|-------|------|-------|--------|
| 1. Bootstrap | 2 | TBD | Not started |
| 2. Smoke Script | 1 | TBD | Not started |
| 3. CI/CD | 1 | TBD | Not started |
| 4. Assertions | 2 | TBD | Not started |
| **Total** | **6** | | |

---

## 9. Approval

| Role | Name | Date | Decision |
|------|------|------|----------|
| Author | | | |
| Reviewer | | | |
| Approver | | | |

---

## 10. Appendix

### A. References

- AWS smoke test: `scripts/smoke-test.sh`
- AWS workflow: `.github/workflows/acceptance.yml`
- Azure TF provider: https://registry.terraform.io/providers/hashicorp/azurerm/latest
- Azure OIDC with GitHub: https://docs.github.com/en/actions/deployment/security-hardening-your-deployments/configuring-openid-connect-in-azure

### B. Related Documents

- `docs/azure-port-design.md` - Azure architecture decisions
- `docs/azure-live-testing-design.md` - This document
- `docs/aws-azure-gaps.md` - Feature parity analysis
