# Azure Live Testing Design

## Current AWS Live Test Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     AWS Live Tests                           │
├─────────────────────────────────────────────────────────────┤
│ Bootstrap (1x per test run)                                  │
│   - VPC                                                      │
│   - ECS Cluster                                              │
│   - ALB (Application Load Balancer)                          │
│   - Subnets, NAT Gateway, etc.                               │
│                                                              │
│ App (1x per example)                                         │
│   - ECS Service + Task Definition                            │
│   - RDS / ElastiCache / S3 (if needed)                       │
│   - ALB Listener Rules                                       │
│                                                              │
│ Verification                                                 │
│   - Poll ALB endpoint                                        │
│   - Check response contains expected string                  │
│   - Assert managed resources created                         │
└─────────────────────────────────────────────────────────────┘
```

## Azure Live Test Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Azure Live Tests                          │
├─────────────────────────────────────────────────────────────┤
│ Bootstrap (1x per test run)                                  │
│   - Resource Group                                           │
│   - Container Apps Environment                               │
│   - VNet + Subnet                                            │
│   - Log Analytics Workspace                                  │
│                                                              │
│ App (1x per example)                                         │
│   - Container App                                            │
│   - PostgreSQL/MySQL Flexible Server (if needed)             │
│   - Redis Cache (if needed)                                  │
│   - Storage Account (if needed)                              │
│                                                              │
│ Verification                                                 │
│   - Poll Container App FQDN                                  │
│   - Check response contains expected string                  │
│   - Assert managed resources created                         │
└─────────────────────────────────────────────────────────────┘
```

## Key Differences

| Aspect | AWS | Azure |
|--------|-----|-------|
| **Entry point** | ALB DNS | Container App FQDN |
| **Load balancer** | Separate ALB resource | Built into Container Apps |
| **Bootstrap** | VPC, ECS Cluster, ALB | Resource Group, Container Apps Env, VNet |
| **State backend** | S3 | Azure Blob Storage |
| **Authentication** | OIDC → AWS IAM | OIDC → Azure Service Principal |

## Implementation Plan

### 1. Azure Bootstrap Terraform

Create `bootstrap/azure/` directory:

```hcl
# bootstrap/azure/main.tf
resource "azurerm_resource_group" "main" {
  name     = var.name
  location = var.location
}

resource "azurerm_log_analytics_workspace" "main" {
  name                = "${var.name}-logs"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  sku                 = "PerGB2018"
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
}

resource "azurerm_container_app_environment" "main" {
  name                       = "${var.name}-env"
  location                   = azurerm_resource_group.main.location
  resource_group_name        = azurerm_resource_group.main.name
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id
  infrastructure_subnet_id   = azurerm_subnet.infrastructure.id
}
```

### 2. Azure Environment YAML Output

```yaml
# bootstrap/azure/environment.yml
name: "{{ .name }}"
region: "{{ .location }}"
target: azure
container_apps_environment_name: "{{ .name }}-env"
log_analytics_workspace_id: "{{ .log_analytics_workspace_id }}"
vnet_id: "{{ .vnet_id }}"
infrastructure_subnet_id: "{{ .infrastructure_subnet_id }}"
container_registry_name: "{{ .acr_name }}"
```

### 3. GitHub Actions Workflow

```yaml
# .github/workflows/azure-acceptance.yml
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
  id-token: write   # OIDC for Azure
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

      - name: Set up Terraform
        uses: hashicorp/setup-terraform@v4
        with:
          terraform_version: "1.10.0"

      - name: Set up uv
        uses: astral-sh/setup-uv@v7
        with:
          python-version: "3.14"

      - name: Smoke test ${{ inputs.example }}
        env:
          NAME: ci${{ github.run_number }}
        run: |
          scripts/smoke-test-azure.sh ${{ inputs.example }}
```

### 4. Azure Smoke Test Script

```bash
#!/usr/bin/env bash
# scripts/smoke-test-azure.sh

# Similar to smoke-test.sh but for Azure:
# 1. Deploy bootstrap (Resource Group, Container Apps Env, VNet)
# 2. Compile with Azure target
# 3. Deploy app
# 4. Poll Container App FQDN
# 5. Verify managed resources
# 6. Teardown

set -euo pipefail

EXAMPLE="${1:-hello}"
NAME="${NAME:-smoke}"
LOCATION="${LOCATION:-eastus}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BOOTSTRAP_DIR="$ROOT/bootstrap/azure"
BUILD_DIR="$ROOT/build/$EXAMPLE"

# ... similar structure to AWS smoke test
```

## Azure Service Principal Setup

### Required Azure Resources

1. **Service Principal** for CI/CD
   ```bash
   az ad sp create-for-rbac \
     --name "composey-acceptance" \
     --role "Contributor" \
     --scopes /subscriptions/{sub-id}/resourceGroups/composey-acceptance \
     --sdk-auth
   ```

2. **Federated Credentials** for OIDC
   - GitHub Actions → Azure OIDC
   - Configure in Azure AD

3. **Resource Group** (persistent)
   - For acceptance test resources
   - Can be cleaned up periodically

### Required GitHub Secrets/Variables

```
AZURE_CLIENT_ID       # Service principal app ID
AZURE_TENANT_ID       # Azure AD tenant ID
AZURE_SUBSCRIPTION_ID # Azure subscription ID
```

## Cost Considerations

| Resource | AWS Cost | Azure Cost |
|----------|----------|------------|
| Bootstrap NAT Gateway | ~$0.05/hr | ~$0.05/hr |
| Container App | ~$0.000024/vCPU/s | ~$0.000024/vCPU/s |
| PostgreSQL (small) | ~$0.02/hr | ~$0.02/hr |
| Test run (30 min) | ~$2 | ~$2 |

Similar cost to AWS.

## Implementation Priority

### Phase 1: Bootstrap (1-2 days)
- [ ] Create `bootstrap/azure/` Terraform
- [ ] Create Azure environment YAML template
- [ ] Test bootstrap manually

### Phase 2: Smoke Test Script (1 day)
- [ ] Create `scripts/smoke-test-azure.sh`
- [ ] Adapt polling logic for Container Apps
- [ ] Test with `hello` example

### Phase 3: CI/CD (1 day)
- [ ] Create `.github/workflows/azure-acceptance.yml`
- [ ] Set up Azure Service Principal
- [ ] Configure OIDC federation
- [ ] Test workflow

### Phase 4: Managed Resource Assertions (1-2 days)
- [ ] Create `scripts/assert_managed_azure.py`
- [ ] Verify PostgreSQL created
- [ ] Verify Redis created
- [ ] Verify Storage created

**Total: 4-6 days of work**

## Recommendation

**Yes, implement Azure live tests.** They provide:
1. Real validation that generated Terraform works
2. Catch provider/API changes early
3. Confidence in Azure support

**Start with:** Bootstrap + `hello` example (minimum viable)
**Then add:** More examples + managed resource assertions
