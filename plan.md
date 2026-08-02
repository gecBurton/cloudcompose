# Go Migration Plan: Composey

## Overview

**Goal:** Migrate composey from Python to Go to achieve native Docker/Terraform ecosystem integration, single binary distribution, and compile-time safety.

**Timeline:** 7 weeks (85 hours part-time)

**Approach:** Incremental migration with parallel testing against Python baseline.

---

## Phase 0: Preparation (Week 1)

### Goals
- Set up Go project structure
- Ensure all Python tests pass
- Document current architecture
- Create parity test harness

### Tasks

**Day 1-2: Audit Python Codebase**
- Document all entry points and function signatures
- Map Python module dependencies
- Identify critical paths (parser → normalizer → inference → generator)
- Document all 13K LOC structure

**Day 3: Set Up Go Project**
- Initialize Go module: `go mod init github.com/gecburton/composey`
- Install core dependencies:
  - spf13/cobra (CLI)
  - compose-spec/compose-go (parsing)
  - hashicorp/terraform-json (generation)
  - hashicorp/hcl/v2 (HCL output)
- Create project structure:
  - cmd/composey/ (CLI entry point)
  - internal/compiler/ (core logic)
  - internal/models/ (data structures)

**Day 4-5: Baseline Testing**
- Run full Python test suite, ensure all pass
- Document expected outputs for all examples (AWS, Azure, GCP)
- Create test fixtures

**Day 6-7: Create Parity Test Harness**
- Create test framework to compare Python vs Go outputs
- Write scripts to run side-by-side comparisons
- Set up CI pipeline for parity testing

### Checkpoint
- ✅ Go project initialized with dependencies
- ✅ Python tests all passing
- ✅ Test harness ready for Go implementation

---

## Phase 1: Port Parser (Week 2)

### Goals
- Parse Docker Compose files using compose-go library
- Eliminate Docker CLI dependency
- Output matches Python version byte-for-byte

### Tasks

**Day 1-2: Implement Parser**
- Use compose-go library for native parsing
- Map compose-go types to internal models
- Handle all compose file fields:
  - services (image, build, ports, environment, depends_on, networks, volumes, secrets)
  - networks
  - volumes
  - secrets
- Extract x-composey extensions

**Day 3: Add Test CLI Command**
- Create `parse` subcommand for testing
- Output parsed data as JSON for comparison
- Add error handling and user-friendly messages

**Day 4-5: Parity Testing**
- Test against all example compose files
- Compare output to Python parser
- Fix discrepancies
- Handle edge cases (empty fields, missing values, YAML vs JSON)

**Day 6-7: Edge Cases & Error Handling**
- Environment variable interpolation
- Build context normalization
- Invalid compose file handling
- Add validation errors

### Checkpoint
- ✅ Parser works with compose-go (no Docker CLI)
- ✅ Output matches Python for all examples
- ✅ Tests pass for parsing

---

## Phase 2: Port Semantic Model & Normalizer (Week 3)

### Goals
- Define Go structs matching Python Pydantic models
- Port all normalization logic
- Type-safe inference

### Tasks

**Day 1-2: Define Go Models**
- Port all models from models/semantic.py
- Port all models from models/compose.py
- Add JSON tags for serialization
- Use pointers for optional fields
- Add validation tags where needed

**Day 3: Port Constants**
- Migrate all constants from constants.py
- Map capability image patterns
- Define size mappings
- Port default values

**Day 4-6: Port Normalizer Logic**
- Port _infer_capability function
- Port _database_name derivation logic
- Port _reject_persistent_volumes validation
- Port _network_segments logic
- Port schedule parsing
- Port auto-scaling configuration parsing
- Port x-composey settings extraction

**Day 7: Validation & Testing**
- Test all normalization rules
- Compare output to Python for each example
- Verify all relationships are captured
- Test edge cases

### Checkpoint
- ✅ Semantic model defined in Go
- ✅ Normalizer produces identical output to Python
- ✅ All inference tests pass

---

## Phase 3: Port AWS Inference & Generator (Week 4-5)

### Goals
- Port AWS resource inference logic
- Generate Terraform JSON using terraform-json types
- Match Python output exactly

### Tasks

**Week 4: AWS Resource Models & Inference**

**Day 1-3: Define AWS Resource Models**
- Port all models from models/aws.py
- Define structs for:
  - ECS (clusters, services, task definitions)
  - RDS (clusters, instances)
  - ElastiCache (clusters)
  - S3 (buckets)
  - IAM (roles, policies)
  - ALB (load balancers, listeners, target groups)
  - CloudFront (distributions)
  - Security groups, networking

**Day 4-7: Port Inference Logic**
- Port infer_networking (security groups, rules)
- Port infer_managed_services (RDS, ElastiCache, S3)
- Port infer_compute_resources (ECS services, tasks)
- Port infer_scheduled_tasks (EventBridge)
- Port infer_edge_resources (CloudFront, WAF)
- Port infer_permissions_and_wiring (IAM roles, policies)
- Port connection string generation

**Week 5: Generator & Testing**

**Day 1-3: Port Generator Logic**
- Use terraform-json for type-safe generation
- Generate Terraform JSON manifest
- Handle provider configuration
- Handle data sources
- Handle resource dependencies

**Day 4-5: Parity Testing**
- Run all AWS examples through Go compiler
- Compare output to Python for each example
- Fix discrepancies in resource naming, references, defaults
- Test edge cases (private services, databases, caches)

**Day 6-7: Error Handling & Edge Cases**
- Handle missing environment variables
- Handle invalid configurations
- Add helpful error messages
- Test error paths

### Checkpoint
- ✅ AWS inference complete
- ✅ Terraform JSON generation works
- ✅ Output matches Python for all AWS examples
- ✅ All AWS integration tests pass

---

## Phase 4: Port Azure & GCP Inference (Week 5-6)

### Goals
- Port Azure resource inference
- Port GCP resource inference
- Multi-cloud parity

### Tasks

**Day 1-3: Port Azure Models**
- Port all models from models/azure.py
- Define structs for:
  - Container Apps
  - PostgreSQL Flexible Server
  - MySQL Flexible Server
  - Redis Cache
  - Storage Accounts
  - Key Vault
  - CDN

**Day 4-7: Port Azure Inference Logic**
- Port database inference (PostgreSQL, MySQL)
- Port cache inference (Redis)
- Port storage inference (Blob Storage)
- Port compute inference (Container Apps)
- Port CDN inference
- Port key vault and secrets management

**Day 8-10: Port GCP Models & Inference**
- Port all models from models/gcp.py
- Define structs for:
  - Cloud Run services
  - Cloud SQL instances
  - Memorystore (Redis)
  - Cloud Storage buckets
- Port GCP inference logic

**Day 11-12: Multi-Cloud Testing**
- Test all Azure examples
- Test all GCP examples
- Compare outputs to Python
- Fix discrepancies

### Checkpoint
- ✅ Azure inference complete
- ✅ GCP inference complete
- ✅ Output matches Python for all clouds
- ✅ Multi-cloud tests pass

---

## Phase 5: Build CLI (Week 6)

### Goals
- Complete CLI with all commands
- Proper error handling and user experience
- Match Python CLI functionality

### Tasks

**Day 1-2: Set Up Cobra CLI**
- Define root command
- Add all flags (file, env, project, out, explain, version)
- Implement version command
- Add bash/zsh completion generation

**Day 3-4: Implement Compile Command**
- Wire up: parse → normalize → compile → write
- Add progress output
- Handle errors gracefully
- Write Terraform JSON to output directory

**Day 5-6: Implement Explain Command**
- Port explain logic
- Format output for terminal
- Show inference decisions
- Display warnings

**Day 7: Environment Loading**
- Parse environment YAML files
- Load AWS/Azure/GCP environment configs
- Validate environment schema
- Handle missing fields

### Checkpoint
- ✅ Full CLI works
- ✅ All commands functional
- ✅ Error messages helpful
- ✅ Version and help text correct

---

## Phase 6: Build & Distribution (Week 7)

### Goals
- Cross-platform binaries
- GitHub releases
- Homebrew formula
- Install script

### Tasks

**Day 1-2: Cross-Platform Builds**
- Build for Linux (amd64, arm64)
- Build for macOS (amd64, arm64)
- Build for Windows (amd64)
- Optimize binary size (ldflags)
- Test on each platform

**Day 3: GitHub Actions Pipeline**
- Create release workflow
- Build binaries on tag push
- Upload artifacts to GitHub releases
- Generate release notes
- Sign binaries (optional)

**Day 4: Homebrew Formula**
- Create homebrew tap repository
- Write formula for macOS/Linux
- Support version pinning
- Publish to homebrew-core or custom tap

**Day 5: Install Script**
- Write `curl | bash` installer
- Detect OS and architecture
- Download correct binary
- Install to /usr/local/bin
- Verify installation

**Day 6: Documentation Updates**
- Update README with Go installation instructions
- Remove Python-specific instructions
- Update examples
- Update AGENTS.md

**Day 7: Final Testing & Release**
- Test all installation methods
- Verify all examples compile
- Test on fresh VM/container
- Release v0.2.0

### Checkpoint
- ✅ Single binary distribution works
- ✅ `brew install composey` works
- ✅ `curl -sSL https://get.composey.ai | bash` works
- ✅ Documentation updated
- ✅ v0.2.0 released

---

## Testing Strategy

### Throughout Migration

**Unit Tests**
- Write Go tests for each function
- Compare output to Python
- Test edge cases explicitly

**Integration Tests**
- Run full pipeline on all examples
- Compare Terraform JSON outputs
- Verify with `terraform plan` (optional)

**Parity Tests**
- For each example:
  1. Run Python compiler → save output
  2. Run Go compiler → save output
  3. Compare (must be identical or explainably different)

**Golden Tests**
- Store expected outputs for each example
- Run on every commit
- Fail if output changes unexpectedly

---

## Migration Tools & Techniques

### AI-Assisted Translation

**Models (30% of codebase):**
- Use Claude/GPT-4 to translate Pydantic models → Go structs
- Manually review and validate
- Time saved: 2-3 days

**Boilerplate (10% of codebase):**
- Use AI for CLI scaffold, constants, helper functions
- Time saved: 1-2 days

### Manual Translation

**Logic (60% of codebase):**
- No shortcuts for inference logic
- Keep Python code open for reference
- Understand logic before translating
- Write idiomatic Go
- Test incrementally

---

## Parallel Operation During Migration

### Strategy

**Weeks 1-6: Dual Codebases**
- Python version remains functional
- Go version built in parallel
- Users can use either (Python recommended during migration)
- Tests verify both produce same output

**Week 7: Switch**
- Go version becomes primary
- Python version marked as deprecated
- Redirect users to Go version

---

## Rollback Plan

### If Critical Issues Found

**Weeks 1-6:**
- Just continue using Python version
- Go migration can pause/resume
- No user impact

**Week 7+:**
- Keep Python version accessible
- Tag as "legacy" but don't delete
- Users can fall back if needed

---

## Success Criteria

### Technical
- ✅ Single binary works on Linux/macOS/Windows
- ✅ No Python dependencies required
- ✅ Parser uses compose-go (no Docker CLI)
- ✅ Output matches Python (byte-identical for same inputs)
- ✅ All tests pass
- ✅ All examples compile successfully

### User Experience
- ✅ `composey --version` works
- ✅ `brew install composey` works
- ✅ `curl -sSL https://get.composey.ai | bash` works
- ✅ Clear error messages
- ✅ Fast execution (5-10x faster than Python)

### Distribution
- ✅ GitHub releases with binaries
- ✅ Homebrew formula published
- ✅ Install script functional
- ✅ Documentation updated

---

## Time Investment

| Phase | Hours | Description |
|-------|-------|-------------|
| Week 1: Preparation | 10 hrs | Setup, testing, harness |
| Week 2: Parser | 15 hrs | compose-go integration |
| Week 3: Normalizer | 15 hrs | Logic port |
| Week 4-5: AWS | 20 hrs | Inference + generation |
| Week 5-6: Azure/GCP | 15 hrs | Multi-cloud |
| Week 6: CLI | 10 hrs | Cobra integration |
| Week 7: Distribution | 10 hrs | Build, release, docs |
| **Total** | **95 hrs** | **~7 weeks part-time** |

---

## Key Dependencies

### Go Libraries

**CLI:**
- github.com/spf13/cobra (CLI framework)

**Parsing:**
- github.com/compose-spec/compose-go (Docker Compose parsing)

**Terraform:**
- github.com/hashicorp/terraform-json (Terraform types)
- github.com/hashicorp/hcl/v2 (HCL generation)

**Utilities:**
- gopkg.in/yaml.v3 (YAML handling)
- encoding/json (standard library)

---

## Risks & Mitigations

### Risk 1: Compose-go API Differences
**Impact:** Parser output differs from Python
**Mitigation:** Extensive parity testing, manual field mapping

### Risk 2: Logic Translation Errors
**Impact:** Inference produces wrong output
**Mitigation:** Test every function, compare all examples

### Risk 3: Cross-Platform Build Issues
**Impact:** Binaries don't work on some platforms
**Mitigation:** Test on VMs for each platform, use CI matrix

### Risk 4: User Resistance to Switch
**Impact:** Users stay on Python version
**Mitigation:** Clear benefits communicated, smooth transition, both versions available

---

## Post-Migration Tasks

### Immediate (Week 8)
- Monitor GitHub issues for bugs
- Respond to user questions
- Fix critical bugs if found

### Short-term (Month 2)
- Optimize performance based on usage
- Add additional examples
- Improve error messages

### Long-term (Month 3+)
- Consider adding HCL output option
- Consider embedded Terraform execution
- Evaluate additional cloud providers
- Build community via examples and docs

---

## Decision Points

### Continue or Abort Criteria

**Continue if:**
- Parity tests pass for each phase
- Performance improvements visible
- Binary size reasonable (< 20MB)

**Abort if:**
- Critical logic errors found
- Compose-go lacks required features
- Time significantly exceeds 10 weeks

---

## References

### Python Codebase
- `composey/cli.py` (172 lines) - CLI entry point
- `composey/compiler/` (~8K lines) - Core logic
- `composey/models/` (~2K lines) - Data models
- `composey/constants.py` (195 lines) - Constants

### Key Python Modules to Port
1. parser.py (77 lines)
2. normalizer.py (372 lines)
3. inference/__init__.py (98 lines)
4. inference/_compute.py (TBD)
5. inference/_managed.py (TBD)
6. inference/_connectivity.py (TBD)
7. generator.py (99 lines)
8. generator_azure.py (TBD)
9. generator_gcp.py (TBD)
10. models/semantic.py (339 lines)
11. models/compose.py (207 lines)
12. models/aws.py (412 lines)
13. models/azure.py (390 lines)

---

## Final Deliverables

**Code:**
- Go codebase (github.com/gecburton/composey)
- All tests passing
- CI/CD pipeline functional

**Binaries:**
- composey-linux-amd64
- composey-linux-arm64
- composey-darwin-amd64
- composey-darwin-arm64
- composey-windows-amd64.exe

**Distribution:**
- GitHub release with binaries
- Homebrew formula
- Install script (get.composey.ai)

**Documentation:**
- Updated README
- Migration guide (for users)
- AGENTS.md updated
- Examples verified

---

## Ready to Start

**Week 1 begins with:**
1. Initialize Go project
2. Install dependencies
3. Run Python tests to establish baseline
4. Create parity test harness

**Time commitment:** 10-15 hours per week for 7 weeks

**You can pause/resume** between phases if needed.

---

## End State

A single Go binary that:
- Parses Docker Compose natively (compose-go)
- Generates Terraform JSON (terraform-json)
- Works on all platforms
- Installs in seconds
- Runs 5-10x faster than Python
- Produces identical output to current Python version

**No more:**
- Python installation
- pip dependencies
- Docker CLI subprocess
- "Works on my machine" issues

**Just:**
```
curl -sSL https://get.composey.ai | bash
composey -f docker-compose.yml -e prod.yaml
```

Done.
