# AWS vs Azure Test Coverage Analysis

## Summary

| Category | AWS Tests | Azure Tests | Gap |
|----------|-----------|-------------|-----|
| **Unit Tests** | 20+ files | 4 files | 🔴 Significant |
| **Integration Tests** | LocalStack, Golden | ❌ None | 🔴 Critical |
| **E2E Tests** | ❌ None | ❌ None | 🟡 Neither has |

## Detailed Comparison

### AWS Test Coverage

**Unit Tests (20+ files):**
- `test_assert_managed.py` - Managed service assertions
- `test_build.py` - Build from source
- `test_capability_detection.py` - Image capability detection
- `test_cdn.py` - CloudFront CDN
- `test_connections.py` - Connection string resolution
- `test_data_retention.py` - RDS snapshot retention
- `test_database_name.py` - Database naming
- `test_desired_count.py` - ECS desired count
- `test_environment.py` - Environment validation
- `test_ingress.py` - ALB ingress rules
- `test_networks.py` - VPC/networking
- `test_permissions.py` - IAM permissions
- `test_platform_config.py` - Platform config
- `test_platform_settings.py` - Platform settings
- `test_robustness.py` - Error handling
- `test_schedule.py` - EventBridge scheduling
- `test_service_discovery.py` - CloudMap
- `test_volumes.py` - Volume handling

**Integration Tests:**
- `test_localstack.py` - Tests against LocalStack (AWS mock)
- `test_golden.py` - Golden file tests (13 examples)

**Total:** ~200+ AWS-specific tests

### Azure Test Coverage

**Unit Tests (4 files):**
- `test_azure_cdn.py` - CDN (3 tests)
- `test_azure_mysql.py` - MySQL (4 tests)
- `test_azure_redis.py` - Redis Cache (3 tests)
- `test_azure_storage.py` - Blob Storage (3 tests)

**Integration Tests:**
- ❌ None

**Total:** 13 Azure-specific tests

## Critical Gaps

### 1. Integration Tests (🔴 Critical)

**AWS has:**
- LocalStack integration tests (tests against real AWS APIs)
- Golden file tests for 13 example applications
- Validates actual Terraform generation

**Azure missing:**
- No Azure equivalent of LocalStack
- No golden file tests for Azure
- No validation of generated Terraform

### 2. Feature-Specific Tests (🔴 Significant)

**AWS has tests for:**
- ✅ Auto-scaling policies
- ✅ Scheduled tasks (EventBridge)
- ✅ Service discovery (CloudMap)
- ✅ IAM permissions and roles
- ✅ Security groups and networking
- ✅ Data retention and snapshots
- ✅ Build from source with ECR
- ✅ Platform configuration

**Azure missing tests for:**
- ❌ Auto-scaling (KEDA triggers)
- ❌ Container Apps Jobs (scheduled tasks)
- ❌ Managed identity permissions
- ❌ VNet integration
- ❌ Log Analytics integration
- ❌ Build from source with ACR

### 3. Cross-Cutting Tests (🟡 Medium)

**Working on both:**
- ✅ Capability detection (image classification)
- ✅ Normalization (compose → semantic)
- ✅ Connection resolution
- ✅ Environment validation

**Not cloud-specific:**
- Parser tests
- Semantic model validation

## Recommendations

### Priority 1: Azure Integration Tests (Critical)

Create Azure golden file tests:

```python
# tests/integration/test_azure_golden.py
@pytest.mark.parametrize("example", AZURE_EXAMPLES)
def test_azure_golden(example):
    """Verify Azure output matches golden files."""
    env = AzureEnvironment(...)
    tf_json = compile_to_terraform(compose_file, env, example)
    # Compare to golden file
```

### Priority 2: Azure Feature Tests (High)

Add tests for:
- Auto-scaling with KEDA triggers
- Managed identity assignments
- VNet integration
- Log Analytics configuration

### Priority 3: Cross-Cloud Tests (Medium)

Create tests that verify:
- Same compose file produces equivalent infrastructure
- Feature parity (where applicable)
- Semantic model consistency

## Current State

| Metric | AWS | Azure | Target |
|--------|-----|-------|--------|
| Unit test files | 20+ | 4 | 4+ (ok for now) |
| Unit tests | ~200 | 13 | 50+ |
| Integration tests | 2 files | 0 | 2 files |
| Golden examples | 13 | 0 | 5-10 |
| Coverage | ~85% | ~30% | ~70% |

## Conclusion

**No, we don't have testing parity.**

Azure has basic unit tests but lacks:
1. Integration tests (critical gap)
2. Feature-specific tests (significant gap)
3. Golden file validation (critical gap)

**Recommendation:** Add Azure integration tests and at least 5-10 golden file examples before considering Azure support production-ready.
