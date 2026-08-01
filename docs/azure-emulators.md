# Azure Local Emulator Options

## Overview

Unlike AWS LocalStack which is mature and widely used, Azure emulators are more fragmented. Here are the options:

## 1. **Azurite** (Microsoft Official)

**What it emulates:**
- Blob Storage
- Queue Storage
- Table Storage

**Pros:**
- ✅ Official Microsoft project
- ✅ Well-maintained
- ✅ Docker support
- ✅ Good API coverage for storage

**Cons:**
- ❌ Only storage services (no compute, database, etc.)
- ❌ Not a full Azure emulator

**Usage:**
```bash
docker run -p 10000:10000 -p 10001:10001 -p 10002:10002 \
  mcr.microsoft.com/azure-storage/azurite
```

**For Composey:** Could test Blob Storage integration

---

## 2. **Azure Functions Core Tools** (Microsoft)

**What it emulates:**
- Azure Functions runtime locally

**Pros:**
- ✅ Official Microsoft tool
- ✅ Good for testing Functions

**Cons:**
- ❌ Only Functions, not other services
- ❌ Not useful for Container Apps

**For Composey:** Not applicable (we use Container Apps, not Functions)

---

## 3. **LocalStack Azure Pro** (Commercial)

**What it emulates:**
- Partial Azure support (newer feature)
- Storage, Service Bus, Event Grid

**Pros:**
- ✅ Same interface as AWS LocalStack
- ✅ Expanding Azure coverage

**Cons:**
- ❌ Requires Pro license ($$$)
- ❌ Azure support is newer/less mature
- ❌ No Container Apps support yet

**For Composey:** Not worth the cost for current feature set

---

## 4. **Mocking/Stubbing** (Custom)

**Approach:**
- Create mock Azure RM provider
- Return fake resource IDs
- Validate Terraform syntax only

**Pros:**
- ✅ Fast
- ✅ No infrastructure needed
- ✅ Can test all resource types

**Cons:**
- ❌ Doesn't test actual Azure API calls
- ❌ Need to maintain mocks

**For Composey:** Already doing this with golden files

---

## Recommendation for Composey

### Current Approach: ✅ Golden Files + Unit Tests

**Why this works:**
1. **Golden files** validate Terraform output matches expected
2. **Unit tests** validate logic without cloud calls
3. **No Azure emulator** needed for core functionality

### What We're Missing

| Testing Level | AWS | Azure | Gap |
|--------------|-----|-------|-----|
| **Syntax validation** | Golden files | Golden files | ✅ None |
| **Logic testing** | Unit tests | Unit tests | 🔴 Azure needs more |
| **API validation** | LocalStack | ❌ None | 🟡 Could add Azurite for storage |
| **E2E testing** | Real AWS | Real Azure | 🟡 Both need this for prod |

### Should We Add Azurite?

**For Blob Storage testing:**
- ✅ Could verify storage account creation works
- ✅ Could test blob operations
- ❌ Doesn't help with Container Apps, databases, etc.

**Verdict:** Not critical for now. Golden files + unit tests provide adequate coverage.

### Future Improvements

1. **More Azure unit tests** (priority: high)
   - Test each inference function
   - Test edge cases

2. **Real Azure testing** (priority: medium, requires Azure subscription)
   - Smoke tests against real Azure
   - Validate generated Terraform actually deploys

3. **Consider Azurite** (priority: low)
   - Only if we need storage-specific validation
   - Add to docker-compose for integration tests

## Conclusion

**No perfect LocalStack equivalent for Azure exists.**

Golden files are actually the better approach for Terraform compilers because:
- They validate exact output
- They're deterministic
- They don't require infrastructure
- They catch regressions

**Keep current approach** (golden files + unit tests) and add more unit tests for Azure.
