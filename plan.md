# Go Migration Plan: Composey

## Overview

**Goal:** Migrate composey from Python to Go to achieve native Docker/Terraform ecosystem integration, single binary distribution, and compile-time safety.

**Timeline:** 7 weeks (85 hours part-time)

**Approach:** Incremental migration with each stage replacing Python code immediately after testing.

---

## ✅ Phase 0: Preparation (Week 1) - COMPLETE

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
  - ~~hashicorp/terraform-json (generation)~~
  - ~~hashicorp/hcl/v2 (HCL output)~~

  Neither of the two struck through above was ever actually installed, and
  neither is correct for what this project needs: the generator emits
  Terraform's JSON syntax via plain dict-to-JSON marshalling
  (`json.dumps` in Python, `encoding/json` in Go), never HCL.
  `terraform-json` parses Terraform plan/state output, it does not
  generate config. Caught during Phase 3 scoping (2026-08-06), left
  struck through here rather than silently deleted so the original plan
  and the correction are both visible.
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

## ✅ Phase 1: Port Parser (Week 2) - COMPLETE

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

## ✅ Phase 2: Port Semantic Model & Normalizer (Week 3) - COMPLETE, then hardened

### Goals
- Define Go structs matching Python Pydantic models
- Port all normalization logic
- Type-safe inference
- **Replace Python parser and normalizer immediately**

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

**Day 7: Integration & Deployment**
- Create hybrid integration layer (`composey/compiler/hybrid.py`)
- Update CLI to use Go parser/normalizer
- Remove Python parser/normalizer files
- Test all examples with Go→Python pipeline

### Checkpoint
- ✅ Semantic model defined in Go
- ✅ Normalizer produces identical output to Python
- ✅ All inference tests pass
- ✅ **Python parser and normalizer removed from production**
- ✅ Go parser/normalizer in production use

### What "complete" actually meant, and what it didn't

Getting `go build`/`go vet`/`go test` clean and the golden tests passing was
not the same thing as the port being correct. Two review passes after the
checkpoint above was marked done found, in order:

**Missing test coverage, not missing behavior.** ~1,700 lines of Python
tests were deleted wholesale in the same commit that removed
parser.py/normalizer.py ("remove unused tests" — they were not unused, they
encoded specific edge cases, several load-bearing). Restoring them properly
— actually running each one against real behavior, not just carrying the
assertions over — surfaced two real regressions the deletion had let through
unnoticed: `env_file` values leaking into deployed Terraform as plaintext
(the one thing `platform_env` exists to prevent), and `explain()` degrading
to three words per service on every real invocation, since the CLI always
calls it with no compose-side model on the Go path. Both fixed; the second
review pass below found the deletion had also cost something else.

**Three further bugs found by asking "is this idiomatic Go, and are we
using compose-go well" rather than "does it pass its own tests."**
Named-volume rejection was completely broken for every real compose file,
short- or long-form syntax — a local lookalike type never matched what
compose-go actually hands back, so a container mounting a named volume
compiled clean with no error at all. The existing test suite had 100%
coverage of the broken type switch and 0% coverage of the path production
actually took, because every test constructed the lookalike type by hand
rather than going through the real parser. Normalize's own output order was
nondeterministic (confirmed: five runs of one file, five different service
orderings), directly violating this project's stated determinism guarantee.
`min_scale: 0` — a legitimate, validated value — was silently overwritten to
`1`. All three fixed; two are exactly the kind of thing "the tests pass"
does not catch, and would not have been caught here either without
deliberately testing through the real parser boundary rather than around it.

**One clear ecosystem-usage gap, also fixed.** `declaredEnvironment` used a
hand-rolled second YAML parse (a direct `gopkg.in/yaml.v3` dependency,
reimplementing compose-go's own environment-form parsing) to work around
`SkipInterpolation` not covering `env_file` merging. compose-go has an
official, if under-documented, way to skip exactly that step
(`loader.Options.SkipResolveEnvironment`) — verified empirically against a
real `.env` file that it does the same job with ~65 fewer lines and no extra
dependency. `compose-go` itself was pinned to the abandoned v1.20.2 module
path at the time; migrated to v2.14.0 immediately after this phase's
hardening pass, before Phase 3 could build further on the v1 surface — see
below.

**Net effect:** the parser/normalizer port is now materially more correct
than when this phase was first marked complete, and the test suite that
exists now (50 Go test functions across 9 focused files, plus two that
specifically go through the real `ParseCompose` boundary rather than
hand-built structs) is a genuinely stronger signal than what existed at the
original checkpoint. This is not a one-time cost: any future phase that
ports Python logic wholesale should assume the same gap exists — passing
tests copied alongside the port prove the port matches the tests, not that
either matches production.

### compose-go v1 → v2 migration — DONE

Done immediately after the hardening pass above, before Phase 3 could add
more code against the v1 surface. Three real, confirmed breaking changes,
all contained to `parser.go` (the only file that imports compose-go
directly — the containment paid off here):

1. `loader.Load` no longer exists; `loader.LoadWithContext` is the closest
   equivalent, and requires a `context.Context`. It also folds in what v1
   needed `loader.Normalize`/`loader.ResolveRelativePaths` as separate
   calls for — both happen internally by default in v2 now.
2. v2 hard-requires a project name at load time ("project name must not be
   empty") where v1 left it for composey's own `Normalize` to assign
   afterward. Worked around with an imperatively-set placeholder at parse
   time, specifically to avoid triggering v2's own directory-name-inference
   fallback — project naming for every cloud already flows entirely through
   the `project_name` argument `Normalize` takes, not through anything
   compose-go itself guesses.
3. `NetworkConfig.External` changed from a struct with its own nested
   `External` field to a plain named bool type.

`types.ServiceVolumeConfig` and `types.ShellCommand` — the two types the
volume and command bug fixes above depended on having an exact shape — are
unchanged in v2; confirmed against the vendored source before migrating,
and re-verified all three fixes (named-volume rejection, env_file/config
splitting, deterministic ordering) against real compose files after the
migration, not just that the existing tests still passed. `go mod tidy`
dropped several v1-era transitive dependencies no longer pulled in by v2.

### Files Changed
**Removed:**
- `composey/compiler/parser.py` (77 lines)
- `composey/compiler/normalizer.py` (382 lines)

**Added (as of the hardening pass, not the original checkpoint):**
- `composey-go/internal/models/semantic.go` (162 lines)
- `composey-go/internal/models/compose.go` (306 lines)
- `composey-go/internal/compiler/normalizer.go` (509 lines)
- `composey-go/internal/compiler/parser.go` (313 lines)
- `composey-go/internal/compiler/constants.go` (116 lines)
- `composey-go/internal/compiler/*_test.go` (9 files, ~1,360 lines)
- `composey/compiler/hybrid.py` (90 lines)
- `composey/compiler/explain.py` (reworked to work from the semantic model
  alone, since the CLI never has a compose-side model on this path)

### Current Architecture
```
User Input (compose.yml)
    ↓
[Go] Parser (compose-go library)
    ↓
[Go] Normalizer (semantic model)
    ↓
[Python] AWS/Azure/GCP Inference
    ↓
[Python] Terraform Generator
    ↓
Output (main.tf.json)
```

---

## ⬜ Phase 3: Port AWS Inference & Generator (Week 4-5) - NOT STARTED

### Scope re-checked before starting (2026-08-06) — larger than planned

Counted before writing any code, rather than discovering mid-phase: AWS
inference + models + generator is **~3,485 lines of Python** across 32
model classes and 31 top-level inference functions
(`composey/models/aws.py`, `composey/compiler/inference/*.py`,
`composey/compiler/generator.py`), verified against 13 golden AWS examples
and 21 unit test files that touch inference or generation. Phase 2's
parser+normalizer, by contrast, was ~940 lines of actual Go logic once
finished (`composey-go/internal/compiler/{parser,normalizer,constants}.go`)
and took 15 hours plus a further ~6 hours of hardening after its checkpoint
was first marked complete. Phase 3 is **roughly 3.7x the code volume** of
what Phase 2 covered, budgeted at 20 hours — worse hours-per-line than what
Phase 2 actually needed, before even accounting for the hardening pass
Phase 2 required. Treat 20 hours as very likely optimistic; re-estimate
once the AWS models are actually ported and the inference logic's real
shape is visible, rather than holding the original estimate as a target.

**The listed Go dependencies for this phase are wrong.** `go.mod` has
neither `hashicorp/terraform-json` nor `hashicorp/hcl/v2`, and neither is
actually what this phase needs: `generator.py` emits plain Terraform JSON
syntax via `json.dumps` on nested dicts — the same `encoding/json` approach
the parser/normalizer already use in Go — never HCL. `terraform-json` is
for *parsing* Terraform plan/state output, not generating it; `hcl/v2`
generates HCL syntax, which this project deliberately never uses ("Terraform
is a compilation target," JSON syntax throughout, per AGENTS.md). Neither
library is needed. Corrected in Key Dependencies below.

### Review discipline for this phase — decided before starting, not after

Phase 2's checkpoint was marked ✅ complete with `go build`/`go vet`/`go
test` clean and golden tests passing — and still shipped three silent bugs
(nondeterministic output, named-volume rejection completely broken, an
explicit `0` silently overwritten) that only a dedicated follow-up review
caught. Applying that lesson concretely to this phase, not just noting it
happened last time:

- **Every ported inference function gets at least one test that goes
  through the real `Normalize()` → `infer()` boundary against an actual
  compose file**, not only hand-built `SemanticApplication`/`Service`
  structs. Hand-built-struct tests stay valuable for edge cases (that's
  how Phase 2's own `normalizer_contract_test.go` is organized) but must
  not be the *only* coverage for any function — that combination is
  exactly what let the volume bug through: 100% coverage of the broken
  code, 0% coverage of the boundary that was actually broken.
- **Check determinism explicitly for every new map iteration.** Run the
  same input through the compiled binary 5+ times, diff the output. Cheap
  to check as each function is written; expensive to find after the fact
  (as Phase 2's nondeterminism bug was).
- **No wholesale test deletion in the same commit as removing the Python
  it tested.** If a Python test's coverage is superseded by a new Go test,
  say so in the commit message with which Go test covers it, the way this
  session's restoration work eventually did — don't delete-and-trust.
- **Before converting any compose-go/AWS-SDK-shaped type into a local
  lookalike struct, check whether the upstream type can be used or
  embedded directly instead.** The volume bug and the dead-field trim
  earlier in this branch's history were both consequences of hand-copying
  a type's shape rather than reusing it; AWS inference will be doing this
  same conversion pattern constantly (Terraform resource attributes,
  potentially AWS SDK types for validation) and should default to reuse
  unless there's a specific reason not to.
- **Re-verify each fix (not just each feature) against real behavior after
  the whole phase's other changes land**, the way this session re-checked
  the volume/determinism/env_file fixes still worked after both the
  compose-go v2 migration and the dead-field trim, rather than assuming
  earlier fixes are permanent just because their own tests still pass.

### Goals
- Port AWS resource inference logic
- Generate Terraform JSON using Go's `encoding/json` (not terraform-json/hcl — see above)
- Match Python output exactly
- **Replace Python AWS inference and generator immediately**

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
- Generate Terraform JSON manifest with Go's `encoding/json` — matching
  generator.py's own approach (nested dicts marshalled to JSON), not a
  dedicated Terraform-authoring library; there isn't one that fits since
  this project outputs Terraform's JSON syntax, not HCL
- Handle provider configuration
- Handle data sources
- Handle resource dependencies

**Day 4-5: Parity Testing**
- Run all AWS examples through Go compiler
- Compare output to Python for each example
- Fix discrepancies in resource naming, references, defaults
- Test edge cases (private services, databases, caches)

**Day 6-7: Integration & Deployment**
- Update hybrid integration to use Go inference for AWS
- Remove Python AWS inference/generator files
- Test all AWS examples with Go→Go→Go pipeline

### Checkpoint
- ✅ AWS inference complete
- ✅ Terraform JSON generation works
- ✅ Output matches Python for all AWS examples
- ✅ All AWS integration tests pass
- ✅ **Python AWS inference and generator removed**
- ✅ **Every item in the review-discipline list above has actually been
  applied, not just available as a checklist** — Phase 2's checkpoint
  looked identical to this one and still needed a follow-up hardening
  pass; do not mark this phase done on the same evidence Phase 2's
  checkpoint was (wrongly) marked done on.

---

## ⬜ Phase 4: Port Azure & GCP Inference (Week 5-6) - NOT STARTED

### Goals
- Port Azure resource inference
- Port GCP resource inference
- Multi-cloud parity
- **Replace Python Azure/GCP inference immediately**

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

**Day 11-12: Integration & Deployment**
- Test all Azure examples with Go pipeline
- Test all GCP examples with Go pipeline
- Remove Python Azure/GCP inference/generator files

### Checkpoint
- ✅ Azure inference complete
- ✅ GCP inference complete
- ✅ Output matches Python for all clouds
- ✅ Multi-cloud tests pass
- ✅ **Full Go compiler in production**

---

## ⬜ Phase 5: Build CLI (Week 6) - NOT STARTED

### Goals
- Complete standalone Go CLI
- Proper error handling and user experience
- Match Python CLI functionality
- **Replace Python CLI with Go binary**

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
- Port full explain logic
- Format output for terminal
- Show inference decisions
- Display warnings

**Day 7: Environment Loading**
- Parse environment YAML files in Go
- Load AWS/Azure/GCP environment configs
- Validate environment schema
- Handle missing fields

### Checkpoint
- ✅ Full standalone Go CLI works
- ✅ All commands functional
- ✅ Error messages helpful
- ✅ Version and help text correct
- ✅ **Python CLI no longer needed**

---

## ⬜ Phase 6: Build & Distribution (Week 7) - NOT STARTED

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

## Incremental Migration Strategy

### Key Principle: Replace Immediately

Instead of running Python and Go in parallel for months:

1. **Port a stage** (parser, normalizer, inference, generator)
2. **Test thoroughly** with parity tests
3. **Replace Python immediately** once tests pass
4. **Remove old code** (git is the rollback mechanism)
5. **Continue to next stage**

### Benefits

- ✅ Go code tested in production immediately
- ✅ No parallel maintenance burden
- ✅ Faster feedback on issues
- ✅ Smaller PRs, easier reviews
- ✅ Can still rollback if needed (`.deprecated/` files)

### Rollback Strategy

**If critical issues are found:**
- Use git to revert to previous commit: `git revert HEAD`
- Or checkout specific files: `git checkout HEAD~1 -- composey/compiler/`
- Tag current state before migration to make rollback easier

**Git is the rollback mechanism - no separate backup directory needed.**

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

| Phase | Hours | Status | Description |
|-------|-------|--------|-------------|
| Week 1: Preparation | 10 hrs | ✅ Complete | Setup, testing, harness |
| Week 2: Parser | 15 hrs | ✅ Complete | compose-go integration |
| Week 3: Normalizer | 15 hrs + ~6 hrs hardening | ✅ Complete | Logic port, **Python removed**; three silent bugs found and fixed by a follow-up idiom/integration review, not caught by the original checkpoint's own tests |
| Week 4-5: AWS | 20 hrs (likely optimistic — see Phase 3's scope note; ~3.7x Phase 2's code volume budgeted at roughly the same hours/line Phase 2 needed *before* its hardening pass) | ⬜ Pending | Inference + generation |
| Week 5-6: Azure/GCP | 15 hrs | ⬜ Pending | Multi-cloud |
| Week 6: CLI | 10 hrs | ⬜ Pending | Standalone Go CLI |
| Week 7: Distribution | 10 hrs | ⬜ Pending | Build, release, docs |
| **Total** | **~101 hrs** | **42% Complete** | **~7 weeks part-time** |

**Progress:** Phase 0-2 complete (40%), Python parser/normalizer removed, Go
in production. Phase 2 required a follow-up hardening pass after its
original checkpoint — see Phase 2's "what 'complete' actually meant"
section above before treating any future phase's checkpoint as sufficient
on its own.

---

## Key Dependencies

### Go Libraries

**CLI:**
- github.com/spf13/cobra (CLI framework)

**Parsing:**
- github.com/compose-spec/compose-go/v2 (Docker Compose parsing) — v2.14.0.
  Migrated from the abandoned v1 module path immediately after Phase 2's
  hardening pass; see Phase 2 above for the three breaking changes involved.

**Terraform:**
- No dedicated library. The generator emits Terraform's JSON syntax by
  marshalling nested structs/maps with Go's own `encoding/json` — the
  same approach the Python generator uses (`json.dumps` on nested dicts)
  and the same package the parser/normalizer already depend on. Originally
  planned as `hashicorp/terraform-json` + `hashicorp/hcl/v2`; neither was
  ever installed, and neither actually fits — `terraform-json` parses
  Terraform plan/state output rather than generating config, and `hcl/v2`
  targets HCL syntax, which this project deliberately never emits (JSON
  syntax throughout — "Terraform is a compilation target," not something
  hand-edited). Corrected during Phase 3 scoping (2026-08-06).

**Utilities:**
- gopkg.in/yaml.v3 — transitive only (via compose-go), not a direct
  dependency of composey's own code. A direct dependency existed briefly
  for a hand-rolled second compose-file parse; removed once compose-go's
  own `SkipResolveEnvironment` was found to do the same job (see Phase 2).
- encoding/json (standard library)

---

## Risks & Mitigations

### Risk 1: Compose-go API Differences
**Impact:** Parser output differs from Python
**Mitigation:** ✅ Extensive parity testing completed, manual field mapping done
**Status:** RESOLVED

### Risk 2: Logic Translation Errors
**Impact:** Inference produces wrong output
**Mitigation:** Test every function, compare all examples — but see below:
passing tests are not sufficient on their own if the tests were carried
over from the same commit that removed the code they were meant to guard,
without being run against the real parser boundary.
**Status:** ENCOUNTERED IN PHASE 2. Three silent bugs (nondeterministic
output order, a validation check that never matched real input, an
explicit `0` value silently overwritten) shipped past `go build`/`go
test`/golden tests and were only found by a dedicated idiom-and-integration
review after the phase was marked complete. Fixed; see Phase 2's "what
'complete' actually meant" above. Treat this as the expected failure mode
for every remaining phase, not a one-off: budget review time per phase
specifically aimed at "does this go through the real boundary," not just
"do the ported tests pass."

### Risk 3: Cross-Platform Build Issues
**Impact:** Binaries don't work on some platforms
**Mitigation:** Test on VMs for each platform, use CI matrix
**Status:** NOT YET APPLICABLE - Phase 6

### Risk 4: User Resistance to Switch
**Impact:** Users stay on Python version
**Mitigation:** ✅ Incremental approach allows testing each stage in production
**Status:** MITIGATED - Already using Go for parser/normalizer in production

---

## Post-Migration Tasks

### Immediate (Week 8+)
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
- ✅ Phase 2 parity tests passed
- ✅ Performance improvements visible (50ms vs 200ms)
- ✅ Binary size reasonable (10MB)
- Next phases show similar results

**Abort if:**
- Critical logic errors found
- Compose-go lacks required features (RESOLVED - works fine)
- Time significantly exceeds 10 weeks

---

## References

###Python Codebase (Before Migration)
- `composey/cli.py` (172 lines) - CLI entry point
- `composey/compiler/` (~8K lines) - Core logic
- `composey/models/` (~2K lines) - Data models
- `composey/constants.py` (195 lines) - Constants

### Go Codebase (After Phase 2, post-hardening)
- `composey-go/cmd/composey/main.go` (105 lines) - CLI entry point
- `composey-go/internal/compiler/` (~940 lines) - Parser + Normalizer
- `composey-go/internal/compiler/*_test.go` (~1,360 lines across 9 files) - tests, split by concern
- `composey-go/internal/models/` (~470 lines) - Data models

### Removed Python Files (Phase 2)
- `composey/compiler/parser.py` (77 lines)
- `composey/compiler/normalizer.py` (382 lines)

---

## Final Deliverables

**Code:**
- Go codebase (github.com/gecburton/composey)
- All tests passing
- CI/CD pipeline functional

**Binaries (Phase 6):**
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

## Current Status (Phase 2 Complete and Hardened)

### What's Working Now

**Parser + Normalizer (Go):**
- ✅ Parses compose files with compose-go (no Docker CLI)
- ✅ Normalizes to cloud-agnostic semantic model
- ✅ Detects capabilities (database, cache, object-storage)
- ✅ Derives database names
- ✅ Validates configurations, including x-composey (unknown-key and
  out-of-range rejection, both enforced by hand since compose-go's schema
  explicitly declines to validate anything under `x-`)
- ✅ Rejects named volumes correctly against real compose-go input (fixed;
  was previously broken for every real compose file — see Phase 2 above)
- ✅ Deterministic output order, independent of Go's own map iteration
  order (fixed; was previously not the case — see Phase 2 above)
- ✅ Splits literal environment values from env_file/${VAR}-sourced ones
  via compose-go's own `SkipResolveEnvironment`, not a hand-rolled second
  YAML parser
- ⚠️ "100% output parity with Python" is no longer claimed outright: parity
  with the Python version that existed at the time of the port is not
  the same claim as "correct against real compose-go input," and the
  three bugs above show those can diverge. Treat parity claims for any
  future phase as needing the same real-parser-boundary verification,
  not just a diff against Python's old output.

**Inference + Generator (Python):**
- ✅ AWS resource inference (ECS, RDS, ElastiCache, S3, ALB, CloudFront)
- ✅ Azure resource inference (Container Apps, PostgreSQL, Redis, Storage,
  Managed Redis, Key Vault, Front Door, Container Apps Jobs, image
  build-and-push via the docker Terraform provider)
- ✅ Terraform JSON generation

**Integration:**
- ✅ Go → Python via hybrid layer
- ✅ All examples compile successfully
- ✅ Production ready for the parser/normalizer stage specifically — most
  of the AWS/Azure inference and generation this depends on has also been
  verified against real cloud deployments (see TODO.md for Azure), not
  just `terraform validate`

### What's Next

**compose-go v1→v2 migration — DONE, before Phase 3 started**
Migrated to `compose-go/v2` v2.14.0. Three breaking changes, all contained
to `parser.go`; full detail in Phase 2 above. Confirmed no regression in
any of Phase 2's own fixes (named-volume rejection, env_file/config
splitting, deterministic ordering) against real compose files after
migrating, not just that the existing tests still passed.

**Phase 3: AWS Inference & Generator**
- Port AWS inference logic to Go
- Port Terraform generator to Go
- Test and replace Python AWS code
- Verify against the real parser/inference boundary at every step, not
  just against golden files copied from the Python version — Phase 2's
  actual lesson, not just its checkpoint
- Target: Week 4-5

---

## End State (After Phase 6)

A single Go binary that:
- Parses Docker Compose natively (compose-go)
- Generates Terraform JSON (encoding/json, not a dedicated Terraform library — see Key Dependencies)
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
