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

## ✅ Phase 3: Port AWS Inference & Generator (Week 4-5) - COMPLETE, Python AWS backend removed

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
- ✅ **Go AWS backend is the default `compile_to_terraform` path** —
  cut over 2026-08-06, verified via 272 Python unit tests, 13 golden
  examples, and a real CLI invocation.
- ✅ **Python AWS inference and generator removed** — `models/aws.py`,
  `generator.py`, and all 6 AWS-specific inference modules deleted the
  same day, once `compile_application`'s AWS branch was confirmed to have
  zero live callers (checked, not assumed) and two genuine cross-cloud
  dependencies were extracted first (see the removal section below). 10
  dependent Python test files removed in the same commit; verified via
  229 passing tests afterward, not left to be discovered broken.
- ✅ **Every item in the review-discipline list above has actually been
  applied, not just available as a checklist** — applied throughout
  porting (real-boundary tests per function, determinism checks, no test
  deletion, reuse-over-lookalike) and caught real bugs before they shipped;
  applied again in a dedicated coverage-gap survey pass across all 13
  relevant Python test files, which closed every scenario-level gap found
  (28 items) before the cutover — the exact kind of follow-up review Phase
  2's checkpoint needed but didn't get until after the fact.

### What's actually done (2026-08-06)

**Ported, in `composey-go/internal/{models,compiler}/`:**
- `models/aws.go` (417 lines) — all 32 resource structs from `models/aws.py`
- `models/environment.go`, `models/terraform.go` — `AwsEnvironment`,
  `TerraformManifest`
- `compiler/common_aws.go`, `connectivity_aws.go` — namespace/priority/
  path-pattern helpers, security groups, service discovery
  (`_common.py`, `_connectivity.py`)
- `compiler/connections_aws.go` — env-var-to-managed-service URL rewriting
  (`connections.py`), including the regex-based host/URL matching
- `compiler/managed_aws.go` — RDS, ElastiCache, S3 (`_managed.py`)
- `compiler/compute_aws.go` — ECS task/service, IAM roles, build-from-source,
  secrets, platform config, ingress, autoscaling (`_compute.py`, the
  largest module at 635 Python lines)
- `compiler/scheduling_aws.go` — EventBridge scheduled tasks (`_scheduling.py`)
- `compiler/edge_aws.go` — CloudFront/WAF (`_edge.py`)
- `compiler/permissions_aws.go` — IAM wiring, confidential-value handling
  (`_permissions.py`, the trickiest module: it mutates already-built task
  definitions in place)
- `compiler/infer_aws.go` — `InferAWS()`, the full orchestration mirroring
  `inference/__init__.py`'s `infer()`
- `compiler/generator_aws.go` — `GenerateAWS()`, provider config, docker/
  CloudFront/WAF conditional wiring
- `compiler/pyjson.go` — see "A new problem class" below
- `compiler/environment_aws.go` — YAML environment file loading for the CLI
- `cmd/composey/main.go` — new `compile-aws <compose-file> --env <yaml>`
  subcommand, doing parse→normalize→infer→generate in one step (chosen
  over accepting pre-normalized semantic JSON as input, to avoid needing
  JSON (de)serialization for the `Schedule` interface type)
- `composey/compiler/hybrid.py` — new `compile_to_terraform_aws_go()`,
  callable but **not yet the default path**: `compile_application`/
  `compile_to_terraform` still call the Python AWS backend. This function
  exists so the Go path is exercised from Python before any cutover, not
  to perform the cutover itself.

**Verified against all 13 AWS golden examples**
(`composey-go/internal/compiler/infer_aws_golden_test.go`), each compared
as parsed JSON (not a raw byte diff, which would pass on coincidental
whitespace matches a structural diff wouldn't) against the Python-generated
`examples/*/expected/main.tf.json`: hello, flask, flask-redis, flask-s3,
minio-s3, build-webapp, scaling, platform-config, compute-tuning,
nginx-flask-mysql, production-stack, web-api, doctor. Also re-verified
end-to-end through the actual `compile_to_terraform_aws_go()` Python→Go
bridge (not just the Go test suite in isolation) for hello, doctor,
production-stack, nginx-flask-mysql, flask-s3, and web-api.

**106 Go test functions** across the new `*_aws_test.go` files (~3,200
lines), the large majority going through the real `ParseCompose()` →
`Normalize()` → `Infer*()` boundary against an actual compose file from
`examples/`, per this phase's own review-discipline rule — not only
hand-built `Application`/`Service` structs, though those remain for edge
cases the golden examples don't happen to cover (e.g. `TestIsDiscoverable`'s
schedule/no-port/database cases). Every inference function that touches a
`map[string]T` got an explicit determinism check (5-6 repeated runs, diffed).

### A new problem class this phase found, that Phase 2 didn't have

Phase 2's three bugs were all about *structure* — wrong type, wrong order,
wrong default. This phase's ported functions build embedded JSON strings
(IAM policies, ECS container definitions, Secrets Manager secret strings)
that Terraform stores as opaque attribute values, and Python's
`json.dumps(dict)` behavior for those turned out to matter at the byte
level in ways `encoding/json` does not reproduce by default:

1. **Key order.** Python dicts preserve insertion order; `json.dumps` never
   sorts unless told to. Every inline policy literal in `_compute.py`,
   `_managed.py`, `_scheduling.py`, `_permissions.py` writes `{"Version":
   ..., "Statement": ...}` in that order. `encoding/json` marshalling a
   `map[string]any` sorts keys alphabetically, flipping this to
   `{"Statement": ..., "Version": ...}` — confirmed as a real, not
   theoretical, divergence by diffing actual Go output against actual
   Python output for the `hello` example (2026-08-06), not caught by any
   golden-file comparison done via `json.loads()` equality, since
   parsed-dict equality doesn't care about key order — only a byte-level
   diff against a live Python run surfaced it.
2. **HTML escaping.** `encoding/json`'s default escapes `<`/`>`, turning
   Terraform's `"~> 5.0"` version constraint into `"~\u003e 5.0"` in the
   output — functionally accepted by Terraform, but not byte-identical to
   Python's `json.dumps`, which was the actual bar for this phase.
3. **Float formatting.** Python's `json.dumps(70.0)` renders `70.0`,
   always with a decimal point; `encoding/json`'s default float handling
   collapses it to `70` once round-tripped through `map[string]any`,
   losing the distinction entirely. Caught via the `production-stack`
   golden example's autoscaling policy `target_value` field.

Fixed with a small purpose-built encoder (`compiler/pyjson.go`:
`PyDumps`/`PyOrdered`/`PyFloat`) rather than reaching for a third-party
ordered-JSON library — the number of call sites needing this is small and
fixed (every inline IAM policy and container definition in this package),
and each one already states its own key order explicitly as Go code, which
is what needed fixing in the first place. `resourceBlocks()` and
`marshalTerraformJSON()` also switched to `json.Decoder.UseNumber()` when
round-tripping through `map[string]any`, so `PyFloat`'s deliberately-formatted
`"70.0"` string survives being decoded back out rather than being
reinterpreted as a plain `float64` and losing the trailing `.0` a second time.

### Other real bugs this phase's review discipline caught before they shipped

- **`EcsService.load_balancer` defaulting wrong.** Pydantic's
  `Field(default_factory=list)` (not `Optional[...] = None`) means Python's
  own output always includes `"load_balancer": []` for a service with no
  public ingress. The Go struct had `omitempty` on that field, silently
  dropping the key. Caught by diffing `nginx-flask-mysql` and
  `compute-tuning` (neither has public ingress) against their golden
  files — both show the empty list Python always writes.
- **`AutoScalingConfig`'s defaults are not an empty configuration.**
  Python's `config = service.auto_scaling or AutoScalingConfig()` reaches
  for a *default_factory* that supplies CPU 70%/Memory 80% metrics and
  300s/60s cooldowns — not "no scaling policies." A bare
  `models.AutoScalingConfig{}` zero value in Go has none of that. Caught by
  `production-stack`, whose `max_scale > min_scale` service relies on
  exactly this default and got zero autoscaling policies in Go before the
  fix, versus two in Python.
- **`eventbridgeExpression` only handled value-typed schedules.** The
  actual normalizer produces `*models.RateSchedule`/`*models.CronSchedule`
  (pointers, per `normalizer.go`), but the type switch only matched the
  value types every hand-built test used — 100% coverage of a path
  production never takes, 0% of the one it does, which is exactly Phase
  2's volume-bug failure mode repeating in a new module. Caught the moment
  `production-stack` (the only golden example with a schedule) was run
  through the real pipeline rather than only through
  `TestEventbridgeExpression_MatchesPython`'s hand-built value-type cases.
- **A wrong assumption about `_store_confidential_value`'s description
  string**, corrected before it shipped by reading the f-string rather
  than guessing: Python's `f"...for {referenced_service}"` renders the
  literal string `"None"` when `referenced_service is None` (an f-string
  calls `str()`), not an empty string or a word like "nothing".

### Test-coverage-gap closure pass (2026-08-06, same day as the port)

The "no systematic pass" gap noted below was closed immediately after: a
dedicated agent enumerated all AWS-inference-relevant scenarios across the
13 relevant Python unit test files (test_build, test_cdn, test_connections,
test_data_retention, test_database_name, test_desired_count, test_ingress,
test_networks, test_permissions, test_platform_config,
test_platform_settings, test_robustness, test_service_discovery — 62
distinct scenarios total) and cross-checked each against the Go test suite
scenario-by-scenario, not just "some test touches this area." Result: 14
scenarios genuinely not covered, 14 more only partially (verified
implicitly via golden examples but never pinned as their own assertion).
All 28 addressed:

**Real gaps that were actually risky (fixed):**
- `AwsEnvironment.Validate()` (rejects `alb_arn` without
  `alb_security_group_id`) — the function existed and was called from
  `LoadAwsEnvironment`, but no test ever exercised either branch.
- `Service.Validate()` (database capability requires `database_name`) —
  same situation: existed, wired into the normalizer, never tested
  directly.
- Discard mode (`retain_data_on_destroy: false`) for S3 (`force_destroy`)
  and ECR (`force_delete`) — no golden example uses this setting, so the
  entire discard branch for those two resource types was untested; only
  the database's discard branch had coverage.
- Permission scoping: a `Relationship` with no actual env-var reference
  must grant nothing, and a grant must be scoped to the specific service
  referenced, not a `Relationship`-only sibling. `InferPermissionsAndWiring`
  never reads `Application.Relationships` at all, so this held by
  construction — but nothing would have caught a future change that started
  consulting it for convenience.

**Real gaps that were narrower but still worth pinning (fixed):**
- Custom `ingress.health_check.path` propagating to the target group
  (every golden example uses the "/" default).
- Listener-rule priority ordering by path specificity within one app, and
  non-collision across two different apps sharing a listener (the literal
  regression `test_two_applications_do_not_collide_on_one_listener` guards
  against: priority was once hardcoded to 100).
- No-explicit-`networks:` compose files producing a single flat
  `default_sg`.
- `AwsEnvironment.LogRetentionDays` actually overriding the 7-day default
  when set (every prior test/example used the default).
- `NamespaceFor`'s underscore-sanitization and mixed-case-lowercasing
  cases (`my_app`→`my-app`, `Prod`/`App`→`prod-app`) — only 2 of Python's 5
  parametrized cases had Go coverage before.
- `desired_count == min_scale` pinned directly against `createEcsService`
  for 3 cases, not only the 2 data points golden examples happened to
  provide.

18 new Go test functions added across `environment_test.go` (new file),
`connectivity_aws_test.go`, `managed_aws_test.go`, `compute_aws_test.go`,
and `permissions_aws_test.go`. Test count: 106 → 130. No bugs found this
pass (unlike the porting pass itself, which found several) — this was
explicitly a coverage-closing exercise, not a new round of feature work,
and the absence of new bugs here is itself informative: it suggests the
port's actual logic was already correct for these paths, only unverified.

### Cutover to the Go AWS backend (2026-08-06, same day as the coverage pass)

`compile_to_terraform()` (the function the CLI and nearly every AWS
integration test actually call) now dispatches to
`compile_to_terraform_aws_go()` for `AwsEnvironment`, instead of the Python
inference/generator pipeline. Azure/GCP are unaffected — they still go
through Go parser/normalizer + Python inference, exactly as before, pending
Phase 4.

`compile_application(app, env)` — the lower-level entry point that takes an
**already-parsed** `Application` object — deliberately still calls the
Python AWS backend for AWS environments, and is documented as doing so
permanently, not as a remaining TODO: the Go `compile-aws` subcommand only
accepts a raw compose file path (parse+normalize+infer+generate in one
step, chosen specifically to avoid serializing the `Schedule` interface
type through JSON — see the CLI subcommand design note earlier in this
phase). There is no way to hand it a pre-built `Application` without
re-parsing a compose file, so `compile_application` cannot cut over without
first solving that, and doesn't try to.

The CLI (`cli.py`) now calls `compile_to_terraform` directly rather than
`parse_and_normalize_go` + `compile_application` separately, which means
AWS compiles now parse and normalize the compose file twice (once for the
`--explain`-style warnings step, once inside the Go subprocess) — flagged
explicitly in a comment at the call site as a real inefficiency, not
hidden. Not fixed this session: fixing it means teaching the Go AWS
backend to accept pre-normalized semantic JSON, which reopens the
Schedule-serialization problem `compile-aws`'s one-step design was chosen
to avoid.

**Verified after the cutover, not merely asserted:**
- All 13 AWS golden examples (`tests/integration/test_golden.py`) pass
  through the new default path.
- 272 Python unit tests pass, including the ones that exercise
  `compile_to_terraform` end-to-end (`test_networks.py`,
  `test_data_retention.py`, `test_platform_config.py`,
  `test_service_discovery.py` — 38 tests across these four files alone,
  all now running through the Go binary rather than Python inference).
  Tests that call `infer()`/`generate()` directly on hand-built objects
  (`test_build.py`, `test_cdn.py`, `test_connections.py`,
  `test_desired_count.py`, `test_ingress.py`, `test_permissions.py`,
  `test_robustness.py`) still exercise the Python code directly, correctly
  — that code hasn't been removed, so those tests still have something to
  test.
- The actual `composey` CLI binary (`python -m composey.cli main --file
  ... --env ... --project ...`), not just pytest, run end-to-end against
  the `hello` example, output confirmed byte-identical to
  `examples/hello/expected/main.tf.json` via direct JSON comparison.
- Go's own test suite (130 tests) still green, unaffected by the Python
  side change, since the cutover only touches `composey/compiler/__init__.py`
  and `composey/cli.py`.

### Python AWS backend removed (2026-08-06, same day as the cutover)

The "permanent dependency" claimed just above turned out to be wrong on
closer inspection, not actually permanent: `compile_application(app, env)`
had **zero live callers** for `AwsEnvironment` anywhere in the
codebase — not the CLI, not `hybrid.py`'s own functions
(`compile_application_hybrid`/`compile_to_terraform_hybrid` turned out to
be dead code too, called by nothing but each other), not any test. The
only thing standing between "cut over" and "removed" was that
`compile_application`'s AWS branch called the Python backend even though
nothing invoked that branch. Checked before deleting anything, not assumed.

Before deleting, two genuine cross-cloud dependencies on the "AWS" files
were found and had to be resolved first, not discovered as breakage after
the fact:
- `composey/compiler/explain.py` (the cloud-agnostic `--explain` reporting
  layer, used before any environment/target is even known) imported
  `_url_pattern` from `compiler/connections.py`. On inspection,
  `connections.py` was never actually AWS-specific — it operates purely on
  `models.semantic.Connection`, a cloud-agnostic type, and already lived
  at the top level of `compiler/` alongside `generator.py`/`hybrid.py`,
  not nested under anything AWS-specific. It was misfiled in this plan's
  own "AWS Python to remove" list, not misfiled in the actual codebase.
  Left in place, correctly.
- `composey/models/azure.py` and `composey/compiler/inference/azure/__init__.py`
  imported `DockerImage`, `DockerRegistryImage`, and `RandomPassword` from
  `models/aws.py` — genuinely shared Terraform-provider-generic resource
  types (Docker build/push, random password generation), reused by Azure's
  own inference because every backend that builds from source or
  provisions a managed database needs the same two Terraform providers.
  Extracted to a new `composey/models/terraform_common.py` before deleting
  `models/aws.py`, and both Azure files' imports updated. Verified via the
  full Azure test suite (66 tests) passing before proceeding.

With those two dependencies resolved, `compile_application` was changed to
raise `NotImplementedError` for `AwsEnvironment` (pointing callers at
`compile_to_terraform`, which already uses the Go backend), matching the
existing else-branch's behavior for genuinely unsupported targets. Then
removed: `composey/models/aws.py`, `composey/compiler/generator.py`,
`composey/compiler/inference/__init__.py`, and all 6 AWS-specific
inference modules (`_common.py`, `_compute.py`, `_connectivity.py`,
`_edge.py`, `_managed.py`, `_permissions.py`, `_scheduling.py`) — along
with the dead `compile_application_hybrid`/`compile_to_terraform_hybrid`
functions in `hybrid.py`.

**10 Python test files removed in the same commit** (per this phase's own
no-wholesale-deletion rule, satisfied here because Go coverage for every
scenario was already verified in the earlier coverage-gap pass, not
assumed): `test_build.py`, `test_cdn.py`, `test_connections.py`,
`test_database_name.py`, `test_desired_count.py`, `test_ingress.py`,
`test_permissions.py`, `test_platform_settings.py`, `test_robustness.py`,
`test_service_discovery.py` — all of them called Python's `infer()`/
`generate()`/`resolve_value()` directly against hand-built objects, not
through `compile_application`/`compile_to_terraform`, so they broke on
import the moment the modules they tested were gone. `test_data_retention.py`
and `test_networks.py`, which test the same behaviors but go through
`compile_to_terraform` (and therefore the Go backend) rather than calling
Python inference directly, were kept and still pass.

**Verified after removal:**
- Full import check across `cli.py`, `compiler/__init__.py`,
  `compiler/hybrid.py`, `compiler/explain.py`, `models/azure.py`,
  `inference/azure`, `inference/gcp`.
- 229 Python tests pass (272 before this pass, minus the 10 removed files'
  worth, which is consistent — nothing broke that wasn't meant to).
- All 13 AWS golden examples still pass through `tests/integration/test_golden.py`.
- Go's own 130-test suite unaffected (this pass touched zero Go files).
- The actual `composey` CLI binary run end-to-end against `doctor` (the
  most complex example: build-from-source, managed DB/cache/bucket,
  confidential secrets), output confirmed byte-identical to
  `examples/doctor/expected/main.tf.json`, including the Python-only
  docker-build-context-copying step working unaffected.
- `compile_application` confirmed to raise `NotImplementedError` with a
  clear message for `AwsEnvironment`, rather than silently doing nothing
  or producing wrong output.

### What's still not done

- **The CLI's double-parse for AWS compiles** (see the cutover section
  above) — correctness is unaffected, but every AWS `composey` invocation
  still runs the Go parser/normalizer twice (once for `--explain`-style
  warnings, once inside the Go `compile-aws` subprocess).
- **`compile_application` cannot support AWS at all**, even in principle,
  without either teaching the Go backend to accept pre-normalized semantic
  JSON (reopening the `Schedule`-interface serialization problem the
  one-step CLI design was chosen to avoid) or building a second, JSON-based
  Go entry point alongside `compile-aws`. Not attempted this session:
  nothing currently needs `compile_application` to support AWS, so there
  was no forcing function to solve it, and inventing one speculatively
  would be solving a problem that doesn't exist yet.
- **`test_assert_managed.py`, `test_cli_env.py`, `test_environment.py`,
  `test_environment_generator.py`, `test_explain.py` were surveyed and
  found not AWS-inference-relevant** (they test the `composey init`
  scaffolding CLI, environment YAML loading/validation as a standalone
  concern, and the `--explain` reporting layer respectively) — correctly
  out of scope for this pass, not silently skipped.

---

## ✅ Phase 4: Port Azure & GCP Inference (Week 5-6) - COMPLETE, Python removed for all three clouds

### Azure: complete, Python removed (2026-08-06)

Ported in one session directly following Phase 3's AWS work, applying the
exact same review discipline (real-boundary tests, coverage-gap survey
against every existing Python test file, byte-identical golden comparison,
cutover only after checking for zero remaining callers before deleting):

**Ported, in `composey-go/internal/{models,compiler}/`:**
- `models/terraform_common.go` — `DockerImage`, `DockerRegistryImage`,
  `RandomPassword` extracted from `aws.go` into a shared file, mirroring
  Python's own `terraform_common.py` extraction (done the same session as
  AWS's Python-file removal): Azure's inference reuses these exact three
  types, not AWS-specific lookalikes, matching Python's own sharing.
- `models/azure.go` (21 resource structs) — full port of `models/azure.py`,
  including every non-empty `default_factory` value found (the class of
  bug AWS's `AutoScalingConfig` 70/80 default caught): `ManagedRedis`'s
  3-key `default_database` dict, `PostgreSQLFlexibleServer`/`KeyVaultSecret`'s
  `lifecycle.ignore_changes` defaults, `FrontDoorRoute`'s
  `patterns_to_match`/`supported_protocols` defaults.
- `models/environment.go` — `AzureEnvironment` added alongside the
  existing `AwsEnvironment`. Confirmed (not assumed) that Python's model
  carries no cross-field validator, unlike `AwsEnvironment`'s ALB check.
- `compiler/azure_naming.go` — `hashlib.sha256(...).hexdigest()[:6]`-based
  name-truncation logic, ported and verified byte-identical against 8
  live Python outputs (including two long-name truncation cases) before
  any test was written, the same discipline applied to AWS's own
  hash-based `priority_band`.
- `compiler/azure_infer.go`, `azure_managed.go`, `azure_compute.go`,
  `azure_edge.go` — the full 859-line `inference/azure/__init__.py`
  ported: managed identity, Key Vault, Container Registry + build/push,
  PostgreSQL/MySQL Flexible Server + private networking (delegated
  subnet + DNS zone + VNet link), Managed Redis, Blob Storage, Container
  Apps + Jobs (scheduling), Front Door CDN.
- `compiler/generator_azure.go` — `GenerateAzure()`.
- `compiler/pyjson.go`/`pyordered_reflect.go` — extended with
  `PyDumpsIndent` and a reflection-based `structToPyOrdered` converter,
  because Azure's `json.dumps(terraform, indent=2)` has **no
  `sort_keys=True` at all**, unlike AWS's generator — every level of the
  entire document, not just embedded policy strings, had to preserve
  Python's insertion order exactly. AWS's approach (round-trip through
  `map[string]any`, rely on `sort_keys` parity) does not work here at
  all; confirmed as a real structural difference, not an oversight, by
  reading generator_azure.py's actual `json.dumps` call before writing
  any Go code for it.
- `cmd/composey/main.go` — new `compile-azure` subcommand, and
  `composey/compiler/hybrid.py` — `compile_to_terraform_azure_go()`,
  sharing subprocess/YAML-writing plumbing with the AWS bridge function
  via a new `_compile_to_terraform_go()` helper rather than duplicating it.

**Real bugs found and fixed, each via diffing actual Go output against
actual Python output — the AWS port's own review discipline repeating,
not degrading, on a second cloud:**
- A stack overflow: `structToPyOrdered`'s default branch returned the
  original (still-pointer) value instead of the dereferenced one for any
  `*string`-typed struct field, causing infinite mutual recursion with
  `writePyValueIndent`. Found immediately when testing against every
  golden example at once, not just `hello`.
- `handleBuildContext`'s build dict (`context`, `platform`, `dockerfile`)
  used a plain `map[string]any`, which is fine for AWS (whose generator
  sorts keys anyway) but wrong for Azure: Python's own dict literal
  states `context`/`platform` first and appends `dockerfile` last, and
  Azure's generator has nothing to fall back on to fix the order.
- `_container_spec`'s Postgres-URL env-var substitution used Go's zero
  values (empty string, `0`) for a `Connection`'s unset optional fields,
  but Python's f-string renders the literal string `"None"` for an unset
  field (`str(None) == "None"`) — confirmed by actually running the
  equivalent Python f-string, not assumed. Ported bug-for-bug per this
  phase's own decision: a Redis/Storage connection substituted into this
  Postgres-shaped template produces a nonsensical URL in both languages
  now, identically, rather than being silently fixed only in Go.
- Connection ordering: Go's `connections` map has no defined iteration
  order, but Python's dict-merge (`_infer_databases()` then
  `.update(cache_connections)` then `.update(storage_connections)`)
  fixes a specific insertion order that determines which `_URL` env var
  comes first when a service references more than one connection.
  Alphabetically sorting the keys (the AWS port's own convention for
  determinism) produced a different, wrong order for `doctor` and
  `production-stack`. Fixed with an explicit `connectionOrderForAzure()`
  that reproduces Python's exact merge order.

**Coverage-gap survey** (mirroring the AWS phase's own dedicated pass):
cross-checked all 66 scenarios across the 8 Azure Python test files
against the Go suite. Found 16 genuinely uncovered and 6 partially
covered — the two largest gaps were the entire MySQL code path (every
"mysql"-named golden example actually uses `mariadb` images, which
`isMySQLImage` classifies as Postgres, so the true MySQL branch had zero
coverage anywhere) and the entire private-networking/delegated-subnet/DNS
-zone path (the golden fixture never sets subnet IDs, so
`privateNetworkingAzure`'s non-fallback branch was dead code as far as
tests were concerned) — plus Azure's own `RateSchedule`→cron conversion
and its rejection error paths, untested in either direction. All 16+6
closed with 19 new Go tests in `azure_coverage_test.go`.

**Cutover and removal, same day:** `compile_to_terraform` now dispatches
to the Go Azure backend for `AzureEnvironment`, exactly like AWS.
`compile_application`'s Azure branch was confirmed to have zero live
callers (same check performed for AWS) before being changed to raise
`NotImplementedError`. Then removed: `composey/models/azure.py`,
`composey/compiler/generator_azure.py`,
`composey/compiler/inference/azure/{__init__.py,naming.py}`, and the 8
Python test files that called `infer()`/`generate()` directly against
hand-built objects. `environment_generator.py` (the *platform* bootstrap
Terraform generator, a different module) was checked first and confirmed
to have no dependency on any of these files.

**Verified:** all 10 Azure golden examples pass through
`TestInferAzure_GoldenExamplesByteIdentical` (structural JSON) and
`TestGenerateAzure_ByteIdentical` (true byte-for-byte, since Azure's own
key ordering is load-bearing unlike AWS's); 165 Go tests total; 173
Python tests pass after removal (209 collected, 66 Azure-specific ones
gone, none of the removed 8 files' scenarios lost — each was confirmed
covered by an equivalent Go test before deletion); a real CLI invocation
against `doctor` (build-from-source, Postgres/MySQL-adjacent database,
confidential secrets) produced byte-identical output to the golden file
after the Python files were gone, not just before.

### GCP: complete, Python removed (2026-08-06), deliberately lighter verification

Ported the same day as Azure, at explicit user direction to move fast and
accept lower rigor: GCP "has never been tested IRL," so this port was not
held to the AWS/Azure standard of a coverage-gap survey against an
existing Python test suite -- because no such suite exists to survey.
Documented as a deliberate scope decision, not a shortcut taken silently:

**Ported, in `composey-go/internal/{models,compiler}/`:**
- `models/gcp.go` (18 resource structs) — direct port of `models/gcp.py`,
  including one genuinely surprising finding worth flagging: Python's
  `GcpResources.random_password` field is typed `Dict[str, Any]`, and
  `_infer_databases` assigns a **bare `{"length": 20}` dict literal**, not
  a `RandomPassword` model instance — unlike AWS/Azure, which both do
  construct a real `RandomPassword`. Reproduced exactly (`map[string]any`
  on `GcpResources.RandomPassword`, not `map[string]models.RandomPassword`)
  rather than assumed to follow the AWS/Azure pattern, which would have
  been wrong here specifically.
- `compiler/infer_gcp.go` — the full 379-line `inference/gcp/__init__.py`
  ported: VPC connector, one **shared** Cloud SQL instance for every
  database-capability service (a real structural difference from AWS/
  Azure's one-server-per-engine approach, ported as-is, not "fixed"),
  Memorystore Redis, Cloud Storage, Cloud Run services. The load-balancer
  step (`_infer_load_balancer`) is a documented Python no-op ("TODO:
  Implement if cdn_enabled or custom domain needed") and was not ported
  as a stub, since there's nothing to port.
- `compiler/generator_gcp.go` — `GenerateGcp()`. Same `PyDumpsIndent`
  approach as Azure (GCP's own `json.dumps` also has no `sort_keys=True`).
  One real structural difference from AWS/Azure worth pinning directly
  (and pinned, in `TestGenerateGcp_AlwaysWiresDockerAndRandomProviders`):
  GCP's provider block unconditionally includes `docker`/`random`,
  regardless of whether anything builds an image or generates a password
  — unlike AWS/Azure's conditional wiring.
- `cmd/composey/main.go` — `compile-gcp` subcommand; `hybrid.py` —
  `compile_to_terraform_gcp_go()`, sharing the same subprocess plumbing as
  AWS/Azure.

**Real bugs found and fixed** — same review discipline applied at smaller
scale (sanity-checking against a handful of hand-run Python outputs for
`hello`/`doctor`/`flask-redis`/`minio-s3`/`production-stack`/
`nginx-flask-mysql`, not an exhaustive golden-example suite, since none
exists):
- The `random_password` dict-vs-model discrepancy above.
- `StorageBucket.Versioning`'s `default_factory=lambda: {"enabled":
  False}` was initially left as Go's `nil` zero value, rendering JSON
  `null` instead of `{"enabled": false}`.
- Connection ordering: the exact same bug class Azure's port hit
  (Python's dict-merge order — databases, then caches, then storage — not
  reproduced by iterating a Go map directly), caught the same way, fixed
  the same way (`connectionOrderForGcp`).

**Verification, explicitly lighter than AWS/Azure:** no golden files exist
to commit as a permanent regression test (none existed in Python to
begin with), so `TestGcp_ByteIdenticalAgainstPython` pins one example's
exact output as a literal string in the test file rather than reading a
committed golden JSON file, and `TestInferGcp_RealExamplesProduceValidJSON`
checks structural validity (a `resource` key, a `provider.google` key)
across 6 real examples rather than byte-identity against all of them.
Byte-identity against live Python was checked manually during the port
for those 6 examples (hello, doctor, flask-redis, minio-s3,
production-stack, nginx-flask-mysql) — real evidence the port is correct
for those cases, but a one-time check, not a repeatable regression test
the way AWS/Azure's golden-file comparisons are. Cut over the same way as
AWS/Azure (`compile_application`'s GCP branch confirmed to have zero live
callers before being folded into a single generic `NotImplementedError`
across all three clouds), then `models/gcp.py`, `generator_gcp.py`, and
`inference/gcp/__init__.py` removed — no dependent Python test files
existed to remove alongside them, confirming the "never tested IRL"
starting point was accurate.

### Checkpoint
- ✅ Azure inference complete
- ✅ GCP inference complete (lighter verification, by deliberate choice)
- ✅ Output matches Python for all three clouds
- ✅ Multi-cloud tests pass (Azure: golden + 66-scenario survey; GCP: 6
  real-example smoke tests + one pinned byte-identical case, reflecting
  GCP's own much smaller pre-existing test surface, not a lapse in this
  pass)
- ✅ **Full Go compiler in production** for AWS, Azure, and GCP — no
  Python inference/generator code remains for any cloud target

---

## ✅ Phase 5: Build CLI (Week 6) - COMPLETE, Python fully removed

### What's actually done (2026-08-06)

Ported the full Python CLI surface to Go in the same session as Phase 4,
applying the same discipline (byte-identical verification against live
Python runs, coverage-gap survey against the existing Python test suite,
real bugs found and fixed by diffing actual output):

**Ported, in `composey-go/{internal/compiler,cmd/composey}/`:**
- `internal/compiler/explain.go` — the full 410-line `compiler/explain.py`
  ported, including **both** branches (with and without the raw compose
  model), even though every current Python caller only ever uses the
  without-branch (`docker_app=None`) — the Python parser that could
  produce a `docker_app` was removed in Phase 2, so that branch is
  presently dead code in both languages. Ported for fidelity per explicit
  direction, not left half-done, and verified working (not just
  compiling) via a real `ComposeApplication` built from `ParseCompose`
  directly, which the Python side has no way to construct anymore.
- `internal/compiler/errors.go` — `ComposeyError`, deliberately not a full
  nine-subclass exception hierarchy: none of Python's `ValidationError`/
  `CompilationError`/`InferenceError`/etc. subclasses are ever
  discriminated on by type in `cli.py`'s exception handling, only the
  base `ComposeyError` is — porting nine unused subclasses would have
  replicated API surface nothing calls, not behavior.
- `internal/compiler/environment.go` — `LoadEnvironment`, the
  target-based dispatcher mirroring `environment.py`'s `load_environment`
  (defaults to "aws" when unset, dispatches to the right cloud-specific
  loader, rejects unsupported targets).
- `internal/compiler/environment_generator.go` (650+ lines) — the full
  `environment_generator.py` ported: `GenerateAwsEnvironment` (VPC,
  subnets, NAT gateways, ALB, ECS cluster — the largest and most
  structurally complex generator in the whole codebase),
  `GenerateAzureEnvironment` (Resource Group, Log Analytics, VNet with 3
  delegated subnets, Container Apps Environment),
  `GenerateGcpEnvironment` (VPC Network, subnet, VPC connector, service
  networking connection).
- `internal/compiler/environment_yaml.go` — `GenerateEnvironmentYAML`,
  including a hand-written PyYAML-compatible scalar-quoting function
  (`pyYAMLScalar`) verified against live `yaml.dump()` output rather than
  assumed from the YAML spec.
- `cmd/composey/compile.go` — the `main` compile command: parse,
  normalize, `--explain`, warnings reporting, compile, write, and Docker
  build-context copying.
- `cmd/composey/init.go` — the `init` command: bootstraps shared platform
  infrastructure for all three clouds.

**Real bugs found and fixed**, each caught by diffing actual Go output
against actual Python output rather than trusting the port:
- `explain.go`'s quoting used Go's `%q` (double-quoted) where Python's
  `!r` uses single quotes throughout every message this file builds —
  caught immediately on the first real comparison, fixed with a `pyRepr`
  helper applied consistently rather than patched at each call site
  individually.
- `environment_yaml.go`'s initial version emitted every value unquoted.
  PyYAML actually quotes a scalar when it contains `": "` (colon-space)
  or ends with `:`, or resolves to a reserved word (`true`/`false`/
  `null`/etc.) — confirmed against a live `yaml.dump()` run for several
  cases (including that an ARN's colons, never followed by a space,
  correctly stay unquoted) before writing the quoting logic, not
  discovered by trial and error afterward.

**Coverage-gap survey** (same discipline as AWS/Azure): a dedicated pass
read all 3 relevant Python test files (`test_explain.py`,
`test_environment_generator.py`, `test_cli_env.py` — ~1,195 lines, 79
test functions total) and cross-checked every scenario against the Go
suite. Found real gaps and closed them: `explain()`'s successful-wiring
decision text, empty-secret warnings, inferred-vs-declared capability
reporting, Dockerfile-path reporting, and the "worth checking" warnings-
present render branch were all previously untested in Go; the AWS
environment generator's ECS cluster/capacity-provider/NAT-gateway/
security-group resource presence, non-default AZ count, non-default VPC
CIDR, hyphenated names, `retain_data_on_destroy=false`, and every ALB
output field were untested; Azure's resource-group/workspace/VNet/
container-app-environment presence and subnet delegation detail were
untested; GCP's subnetwork/service-networking resources and output
fields were untested. All closed with new tests, verified passing.
`environmentTarget`/`compileTerraform`/`copyDir`/`copyDockerBuildContexts`
in `cmd/composey` (previously zero test coverage anywhere, Go or Python,
since Typer's `CliRunner`-based tests don't have a Go analog for
`os.Exit`-calling command handlers) got direct unit tests instead,
following the same pattern `compile.go` already used for pure-function
extraction.

**Not refactored for testability:** `init.go`'s inline provider-
validation/tags-parsing/region-defaulting logic still calls `os.Exit`
directly rather than being extracted into pure functions the way
`compile.go`'s `environmentTarget`/`compileTerraform` are — a deliberate
choice (not an oversight) given the low complexity of that specific glue
code versus the indirection a refactor would add, made explicitly rather
than assumed.

**Verified:** 225 Go tests pass across `internal/compiler`,
`internal/models`, and (newly) `cmd/composey`. Real end-to-end CLI runs
(not just `go test`) for `main --explain`, `main` with AWS/GCP
environments (byte-identical output to golden files), and `init` for all
three clouds, confirmed working after the port, including Docker
build-context copying for the `doctor` example.

### Python fully removed (2026-08-06, same session as the CLI port)

Before deleting anything, the Go CLI was verified to be a complete
replacement, not just a plausible one: every example in `examples/` was
run through `composey-go`'s `main` command directly (no Python, no
`uv run`, no subprocess bridge) for AWS, Azure, and GCP, and every output
matched the corresponding `expected/` golden file exactly. Only after
that did removal proceed.

**Removed:**
- The entire `composey/` Python package (32 source files: `cli.py`,
  `cli_env.py`, `compiler/` including all AWS/Azure/GCP inference and
  generator modules, `models/`, `constants.py`, `exceptions.py`,
  `environment_generator.py`) — all of it either already dead (superseded
  in Phases 3/4) or, for the CLI files specifically, verified redundant
  in this pass.
- The entire `tests/` directory (30+ Python test files) — nothing left
  to test once the module tree they tested was gone.
- `pyproject.toml`, `uv.lock`, and the local `.venv` — no Python runtime
  dependency remains anywhere in the repository.

**Updated, not deleted, since they still have a real job:**
- `scripts/smoke-test.sh` / `scripts/smoke-test-azure.sh` — real-AWS/
  real-Azure end-to-end acceptance scripts. Previously called `uv run
  composey`; now build `composey-go` fresh and call that binary directly.
  Also dropped the `docker-compose-v2` apt step both scripts had, which
  existed for the old Python parser's `docker compose config` shell-out
  — the Go parser uses `compose-go` as a library, with no CLI dependency,
  so nothing in either script actually needed it once checked.
- `.github/workflows/ci.yml` — rewritten to set up Go only (`go vet`,
  `go test`, a build step), with the `uv`/ruff/pytest steps removed
  entirely rather than left disabled.
- `.github/workflows/acceptance.yml` / `azure-acceptance.yml` — swapped
  their `uv`/Python setup steps for a Go setup step; the workflows
  themselves still just call the (now Go-calling) smoke-test scripts.
- `Makefile` — `format`/`vet`/`test`/`build` targets now run `gofmt`/
  `go vet`/`go test`/`go build` against `composey-go`, replacing the
  `uv run ruff`/`uv run pytest` targets.
- `README.md` — installation instructions, the "Quick Start" flow, the
  `--explain` example output, and the "Project Status" footer updated to
  match the actual Go CLI (`composey main`/`composey init`/`--explain`
  flag) rather than the aspirational `composey up --provider aws`/
  `composey explain` shape the README described but that was never built
  in either language. The rest of the README (mission statement,
  philosophy, feature descriptions) was left alone — a targeted fix, not
  a full rewrite, per an explicit scoping decision for this pass.
- `AGENTS.md` — fully rewritten. It described the old Python architecture
  end to end (Pydantic models, Typer CLI, `uv`/pytest workflow); every
  section now describes the actual Go codebase structure, error-handling
  convention (`ComposeyError`), and testing/debugging commands.

**Deleted outright, not updated:**
- `scripts/compare-clouds.py` — imported `composey.compiler` directly
  (the package being removed), had a hardcoded absolute path, and
  contained a pre-existing Python 3 syntax error (`except KeyError,
  TypeError:`, invalid since Python 3.0) meaning it did not actually run
  before this pass either. Not worth porting a broken one-off dev script.

**Left alone, deliberately:**
- `scripts/assert_managed.py` — pure-stdlib Python (`json`, `sys` only),
  reads `terraform show -json` from stdin. No dependency on the removed
  `composey` package, so it keeps working unmodified; Python isn't being
  eliminated from the *repository's tooling*, only from the *product*
  itself.
- `ci/` (real Terraform state for acceptance-test infrastructure) and
  `docs/` (design docs) — unrelated to the Python/Go question entirely.

**Verified after removal:** `go build`/`go test`/`go vet` all pass;
`make build`/`make test` (the new Makefile) work; every example in
`examples/` re-verified byte-identical against its golden file through
the `make`-built binary specifically (not a binary built by some other
means earlier in the session), for AWS, Azure, and GCP.

### What's still not done

- **No installable distribution.** There is no `pip install composey`,
  `brew install composey`, or downloadable release binary — building
  from source with `go build` is the only install path today. This is
  explicitly Phase 6's scope, not a gap in this phase.
- **No bash/zsh completion generation.** Cobra supports this natively
  (`rootCmd.GenBashCompletion`, etc.) but it was not wired up in this
  pass — a small remaining task, not a structural gap.
- **`init.go` not refactored for direct unit-testability** (deliberate,
  per the earlier decision in this phase — see the CLI port notes above).

### Checkpoint
- ✅ Full standalone Go CLI works (`main`, `init`, `parse`, `normalize`,
  `compile-aws`/`compile-azure`/`compile-gcp`, `version`)
- ✅ All commands functional, verified via real end-to-end runs
- ✅ Error messages match Python's wording (missing --env, missing
  compose file, invalid --tags JSON, unsupported provider)
- ✅ Version and help text present (version differs deliberately from
  Python's package version — the Go binary versions independently)
- ✅ **Python CLI no longer needed** — and, going further than the
  original checkpoint asked, no longer present: the Python package,
  its test suite, and its packaging metadata are gone. CI, smoke-test
  scripts, the Makefile, README, and AGENTS.md were all updated to match
  rather than left pointing at deleted code.

---

## ✅ Idiomatic-Go Cleanup Pass (post-Phase 5, 2026-08-07) - COMPLETE

Once the Go port was complete and Python fully removed, the codebase
still carried structural residue from being a line-by-line port: helper
types and functions built specifically to reproduce Python's `dict`
insertion-order and `json.dumps()` formatting, and several models using
`any`/`map[string]any` as an escape hatch for what should have been typed
structs. This pass removed that residue and converted the remaining
Python-shaped code to idiomatic Go, without changing any generated
Terraform output's meaning.

### Goals
- Remove code that existed only to match Python's `dict` behavior, now
  that there is no Python output left to match.
- Replace `any`/`map[string]any` escape hatches in `internal/models/azure.go`
  and `internal/models/gcp.go` with real typed structs.
- Evaluate whether a HashiCorp/Terraform-provided Go SDK could replace
  the hand-rolled Terraform JSON structs.
- Fix any real bugs found along the way, without silently "fixing"
  faithfully-ported Python behavior that turns out to be correct/deployed.
- Do not lose test coverage — rewrite tests that were checking
  Python-matching behavior (byte-for-byte, key order) into tests that
  check the actual thing that matters (structural/semantic correctness).

### What was found
- `pyjson.go` / `pyordered_reflect.go` (466 lines) existed solely to
  reproduce Python's dict insertion-order in JSON output. Terraform's
  JSON syntax is order-agnostic, and `encoding/json.Marshal` already
  sorts map keys alphabetically, so this was pure legacy weight once
  there was no Python output to diff against. Deleted outright.
- No Terraform Go SDK evaluation turned up a usable replacement:
  HashiCorp does not publish provider resource schemas as importable Go
  structs (they live inside compiled provider binaries, generated from
  the provider's own Go source at build time). Decision: keep hand-rolled
  structs with `json` tags matching Terraform attribute names exactly —
  this is the same approach `generator_aws.go` already used successfully.
- 3 of 6 tests named `*_ByteIdenticalAgainstPython` never actually did a
  byte comparison — misleading names inherited from the port, not
  correctness bugs, but worth fixing while touching this code.
- Two real Terraform-schema-shape bugs, found while typing Azure's `any`
  fields against the *actual* `azurerm_container_app`/
  `azurerm_container_app_job` provider docs:
  - `ContainerAppIngress.TrafficWeight` needed to be a bare object, not
    a one-element array (Terraform's JSON syntax accepts either for a
    single nested block per the [JSON syntax spec's Nested Block
    Mapping rules](https://developer.hashicorp.com/terraform/language/syntax/json),
    but the struct wasn't consistent with what the golden fixtures —
    verified against real Azure deployments per `docs/azure-todo.md` — already
    used).
  - `ContainerAppJob`'s `schedule_trigger_config`/`template` needed to be
    `[]T` (arrays), the opposite shape from `ContainerApp`'s equivalent
    fields. This asymmetry between the two resource types is real and
    intentional, not a bug to unify — confirmed against the deployed
    golden fixtures rather than assumed.
  - **False start, caught and reverted:** before confirming the above,
    all 10 Azure golden fixtures were edited (via a one-off script) on
    the wrong assumption that the bare-object form was itself the bug.
    Reverted via `git checkout examples/*/expected/azure/main.tf.json`
    once the JSON syntax spec and provider docs were actually checked;
    the Go struct shapes were fixed instead, matching the deployed
    fixtures.
- The AWS golden test never re-parsed embedded JSON-string Terraform
  attributes (`container_definitions`, `assume_role_policy`, `policy` —
  attributes whose *value* is itself a JSON string). Once embedded-JSON
  formatting changed from Python's spaced/insertion-order style to Go's
  compact/alphabetical style, the test started failing on purely
  cosmetic differences inside those strings. Fixed with a
  `normalizeEmbeddedJSON()` helper that re-parses any string value that
  itself decodes as JSON before comparing.

### What was done
- Deleted `pyjson.go` and `pyordered_reflect.go`.
- Rewrote `generator_azure.go` and `generator_gcp.go` to use plain
  `map[string]any` + `encoding/json`, matching `generator_aws.go`'s
  existing pattern; generalized `generator_aws.go`'s helpers
  (`structResourceBlocks`, `marshalIndentedJSON`, `marshalJSONStringPlain`)
  so all three clouds and `environment_generator.go` share them.
- Added `internal/compiler/iam_policy.go` (`IAMPolicyDocument`,
  `IAMPolicyStatement`, `newIAMPolicyDocument()`, `marshalJSONString()`),
  replacing ~8 duplicated ordered-dict IAM policy constructions across
  `compute_aws.go`, `scheduling_aws.go`, `permissions_aws.go`,
  `managed_aws.go`.
- Typed the `any` escape hatches in `internal/models/azure.go` (11 new
  structs, e.g. `ContainerAppTemplate`, `ContainerAppIngress`,
  `ContainerAppTrafficWeight`, `ContainerAppJobScheduleTrigger`) and
  `internal/models/gcp.go` (11 new structs, e.g. `CloudRunTemplate`,
  `CloudRunContainer`, `CloudSqlBackupConfiguration`); fixed
  `GcpResources.RandomPassword` to use the shared `models.RandomPassword`
  struct instead of `map[string]any`.
- Rewrote `azure_compute.go` and `infer_gcp.go`'s inference functions to
  build the new typed structs directly; removed the now-dead
  `anyMapsToPy()` helper.
- Simplified `permissions_aws.go`'s container-definitions handling —
  removed `rebuildContainerDefinitionsJSON`/`anyToPyValue` (47 lines) in
  favor of the one-line `json.Marshal` call that was already being
  computed and discarded.
- Rewrote `environment_generator.go` (~640 lines, the `composey init`
  shared-infra generators) from ordered literals to plain maps; removed
  the now-dead `tagOrder` parameter from `GenerateAwsEnvironment`/
  `GenerateAzureEnvironment`/`GenerateGcpEnvironment` (kept in
  `GenerateEnvironmentYAML`, where it's still genuinely needed for
  deterministic YAML key ordering) and updated all call sites in
  `cmd/composey/init.go` and its tests.
- Renamed and rewrote misleadingly-named or now-stale Python-matching
  tests to check structural/semantic correctness instead of byte-for-byte
  or key-order matches: `TestEcsTasksAssumeRolePolicy_KeyOrderMatchesPython`
  → `TestEcsTasksAssumeRolePolicy_HasExpectedStatement`,
  `TestHandleAutoscaling_TargetValueRendersAsFloat` →
  `TestHandleAutoscaling_TargetValueIsCorrectNumber`,
  `TestGcp_ByteIdenticalAgainstPython` → `TestGcp_MatchesExpectedStructure`,
  three `environment_generator_test.go` tests renamed from
  `*_ByteIdenticalAgainstPython` to `*_ValidStructure`, and
  `TestGenerateAzure_ByteIdentical` removed as redundant with existing
  golden-file coverage.
- Cleaned up stale Python-referencing comments in `generator_aws.go` and
  `AGENTS.md` (file-tree diagram, "Determinism is critical" section).

### Deferred, by explicit decision, not started
- Splitting `internal/compiler` (~30 non-test files, one flat package)
  into `internal/compiler/{aws,azure,gcp}` sub-packages.
- Auditing/reducing the ~266 exported identifiers in `internal/compiler`
  for over-exporting (most are only ever used internally or by
  same-package tests).

### Checkpoint
- ✅ `gofmt -l .` clean, `go vet ./...` clean, `go build ./...` clean
- ✅ `go test ./...` — 224 tests passing
- ✅ Manual end-to-end CLI run re-verified semantically identical output
  against golden fixtures for AWS (embedded-JSON-string differences are
  cosmetic only, confirmed via recursive normalization)
- ✅ Two real Azure struct-shape bugs found and fixed against actual
  deployed golden fixtures, not assumed
- ✅ No test coverage lost; misleadingly-named tests renamed/rewritten
  rather than deleted

---

## ✅ Package Split: `internal/compiler` → per-cloud sub-packages (post-cleanup-pass, 2026-08-07) - COMPLETE

Explicitly deferred at the end of the idiomatic-Go cleanup pass above,
then done as a follow-up in the same day: `internal/compiler` was one
flat package with ~30 non-test files mixing AWS/Azure/GCP inference and
generation code together, which was the main reason so many identifiers
had to be exported in the first place (same-package access needs no
export, but nothing was actually cloud-isolated at the package level to
test that assumption against). This pass split it into
`internal/compiler/{aws,azure,gcp}` sub-packages plus a new
`internal/compiler/shared` leaf package, without changing any generated
Terraform output.

### Investigation before starting

A dedicated audit (via a research-only subagent pass, no files touched)
confirmed the AWS/Azure/GCP inference and generator files themselves have
**zero real code-level cross-cloud dependencies** on each other — every
"aws"/"azure"/"gcp" string match inside another cloud's file was a
documentation comment explaining an intentional divergence, never an
actual call. The real work was around the shared orchestration/helper
layer:
- Four generic JSON-marshalling helpers (`structResourceBlocks`,
  `marshalTerraformJSON`, `marshalIndentedJSON`, `marshalJSONStringPlain`)
  were physically defined inside `generator_aws.go` but called from
  Azure's and GCP's own generators, and from the shared-infrastructure
  generators for all three clouds — despite having no AWS-specific
  content at all.
- `iam_policy.go` lived among the "shared-looking" files but is actually
  AWS-only (IAM is an AWS concept; nothing else used it).
- `environment_generator.go` was a single 668-line file containing all
  three clouds' `composey init` shared-infrastructure generators mixed
  together, sharing six generic CIDR-math/tag-merging helpers
  (`tfName`, `cidrsubnet`, `ipToUint32`, `uint32ToIP`, `mergedTags`,
  `nameEnvTag`) that had to be factored out before any cloud-by-cloud
  split was possible.
- `environment_yaml.go`'s `GenerateEnvironmentYAML` turned out to be
  AWS-only content (hardcoded `target: aws`, `--provider aws` in its own
  usage strings) sitting at the shared root, not genuinely cross-cloud.
- `constants.go` mixed genuinely shared constants (`DatabaseDefaultUsername`,
  port defaults) with AWS-only ones (`SizeMappings`, `DBInstanceClasses`,
  `PriorityBands`) in one file — kept together rather than split further,
  since nothing forced a split and over-splitting constants for its own
  sake wasn't worth it.
- A handful of helpers were discovered only once the actual build broke
  post-move, not found by the earlier audit: `sortedKeys` (env-var map key
  sorting, used by both AWS and Azure), `urlPattern` (URL-host regex
  matcher, used by AWS's connection inference *and* the cloud-agnostic
  `--explain` reporting), and `asRateSchedule`/`asCronSchedule` (Schedule
  type-assertion helpers, same dual use). All three were genuinely
  cloud-agnostic despite living inside AWS- or Azure-specific files.

### The real blocker: an import cycle, and how it was resolved

The naive plan (`internal/compiler` root imports `aws`/`azure`/`gcp` for
`LoadEnvironment`'s dispatch, while `aws`/`azure`/`gcp` import the root
for shared constants/JSON helpers/parser/normalizer) is a cycle, not a
layering problem that resolves itself. Presented to the user as an
explicit decision point rather than guessed past silently. Chosen fix: a
strict three-tier layout —
- `internal/compiler/shared` — a true leaf package with no dependency on
  any cloud or on the orchestration root. Ended up holding: constants,
  `ComposeyError`, the JSON-marshalling helpers, CIDR/tag helpers,
  `SortedKeys`, `URLPattern`, `AsRateSchedule`/`AsCronSchedule`, and —
  once the same cycle problem showed up a second time for test files —
  `ParseCompose`/`ParseComposeJSON` and the entire normalizer
  (`Normalize`, `InferCapability`, `DatabaseName`, `SemanticToJSON`,
  etc., moved wholesale since they're genuinely cloud-agnostic and had
  no dependency on anything cloud-specific).
- `internal/compiler/{aws,azure,gcp}` — import `shared`, never each
  other, never the root.
- `internal/compiler` (root) — orchestration only now: `LoadEnvironment`
  (imports all three cloud packages to dispatch on declared `target:`),
  `Explain`/`Render`/`StripMarkup` (cloud-agnostic --explain reporting),
  and thin re-export wrappers for `ParseCompose`/`Normalize`/
  `SemanticToJSON` so `cmd/composey` (which needs the root's `Explain`
  and the cloud packages' `Infer*`/`Generate*` in the same functions
  already) didn't have to change its own import shape.

  A second, narrower cycle surfaced only once real-boundary tests in
  `aws`/`azure`/`gcp` (which parse+normalize actual `examples/*/compose.yml`
  fixtures, not hand-built structs, per this project's own testing
  convention) tried to import the root `compiler` package for
  `ParseCompose`/`Normalize` — since the root now imports those same
  cloud packages. Resolved by having those test files call
  `shared.ParseCompose`/`shared.Normalize` directly instead of going
  through the root's re-export wrapper, which only `cmd/composey` needed
  in the first place.

### What was done

- Created `internal/compiler/shared` and moved into it: `constants.go`,
  `errors.go` (`ComposeyError`), a new `terraform_json.go`
  (`StructResourceBlocks`, `MarshalIndentedJSON`, `MarshalJSONStringPlain`
  — exported since multiple packages now call them), a new
  `environment_helpers.go` (`TfName`, `Cidrsubnet`, `MergedTags`), a new
  `sorted_keys.go` (`SortedKeys`), a new `url_pattern.go` (`URLPattern`),
  a new `schedule.go` (`AsRateSchedule`/`AsCronSchedule`), and — once the
  test-file cycle above was found — `parser.go` and `normalizer.go`
  wholesale, plus every test file that exercised them
  (`build_test.go`, `capability_test.go`, `database_name_test.go`,
  `ingress_test.go`, `networks_test.go`, `normalizer_contract_test.go`,
  `normalizer_test.go`, `platform_settings_test.go`, `schedule_test.go`,
  `volumes_test.go`, `xcomposey_test.go`, `testhelpers_test.go`).
- Created `internal/compiler/aws` (14 non-test files) from
  `common_aws.go`, `compute_aws.go`, `connections_aws.go`,
  `connectivity_aws.go`, `edge_aws.go`, `infer_aws.go`, `managed_aws.go`,
  `permissions_aws.go`, `scheduling_aws.go`, `generator_aws.go`,
  `iam_policy.go`, `environment_aws.go`, `environment_yaml.go`, plus a
  new AWS-only `environment_generator.go` split out of the old
  mixed-cloud file — all renamed to drop the now-redundant `_aws` suffix
  (e.g. `compute_aws.go` → `aws/compute.go`) and repointed at
  `shared.*` for every constant/helper the investigation above found.
- Created `internal/compiler/azure` (8 non-test files) the same way from
  `azure_compute.go`, `azure_edge.go`, `azure_infer.go`, `azure_managed.go`,
  `azure_naming.go`, `generator_azure.go`, `environment_azure.go`, plus a
  new Azure-only `environment_generator.go`.
- Created `internal/compiler/gcp` (4 non-test files) the same way from
  `infer_gcp.go`, `generator_gcp.go`, `environment_gcp.go`, plus a new
  GCP-only `environment_generator.go`.
- Split `environment_generator_test.go` (752 lines, all three clouds
  mixed) into `aws/environment_generator_test.go`,
  `azure/environment_generator_test.go`,
  `gcp/environment_generator_test.go`, plus
  `shared/environment_helpers_test.go` for the `TfName`/`Cidrsubnet`
  tests that used to live alongside them.
- Fixed every real-boundary test's relative path to `examples/*/compose.yml`
  fixtures for the new directory depth (`../../../examples/...` →
  `../../../../examples/...` from `aws`/`azure`/`gcp`; `../../examples/...`
  → `../../../examples/...` from `shared`) — caught by running the full
  suite, not by inspection, since a wrong relative path fails at test run
  time with a clear "no such file or directory", not at compile time.
- Updated `cmd/composey/{main,compile,init}.go` to import
  `internal/compiler/aws`/`azure`/`gcp` alongside the root
  `internal/compiler`, matching the CLI's existing per-cloud dispatch
  shape (one `compile-<cloud>` subcommand each in `main.go`; a type
  switch over the loaded environment in `compile.go`; a provider switch
  in `init.go`) — confirmed via the earlier audit that every CLI entry
  point already needed to know about all three clouds simultaneously, so
  this was the expected shape, not a design smell to fix.
- Updated `AGENTS.md`'s file-tree diagram to reflect the new package
  layout.

### Checkpoint
- ✅ `gofmt -l .` clean, `go vet ./...` clean, `go build ./...` clean
- ✅ `go test ./...` — 224 tests passing, same count as before the split
  (no coverage lost or silently dropped in the file shuffle)
- ✅ AWS and Azure `*_GoldenExamplesByteIdentical` tests re-verified
  passing against every example in `examples/` through the new package
  boundaries
- ✅ Confirmed no real cross-cloud code dependency existed before
  starting (only comments), and none was introduced by the split
- ✅ Import cycle identified and resolved architecturally (three-tier
  `shared` → `{aws,azure,gcp}` → root layout) rather than papered over
  with a workaround

### Deferred, still not done
- The ~266-export over-exporting audit from the previous cleanup pass is
  largely moot now: most of what was exported only because same-package
  tests needed it is now exported because a *different package*
  genuinely needs it (aws/azure/gcp calling into `shared`, or the root
  calling into all three clouds). A fresh, smaller audit of what's
  exported *within* each of the four new packages but never used outside
  it would still be worth doing, but was not done in this pass.

---

## ✅ Terraform Schema Verification Tool (`cmd/schema-check`, 2026-08-07) - COMPLETE

Prompted by a user question ("are we using Terraform as well as we
could — could we use its API more directly, or to write the JSON for
us?"). Investigated and rejected two alternatives before building this:

- **A Terraform Go SDK for authoring config**: doesn't exist. HashiCorp
  publishes `hashicorp/terraform-json` (parses `terraform show -json`
  plan/state output — the opposite direction from generating config) and
  the provider plugin protocol (for *implementing* a provider, not
  consuming one). Neither helps write config.
- **CDKTF**: does generate typed, schema-accurate provider bindings you
  author against — genuinely closer to "Terraform via an API" — but its
  Go support is jsii-generated bindings around a TypeScript core, which
  would reintroduce exactly the non-Go runtime dependency the Python
  migration and idiomatic-Go cleanup pass both spent effort removing.
  Rejected for that reason, not on technical merit.

What's genuinely available and useful: `terraform providers schema
-json` — the authoritative, versioned schema for every resource/attribute
a provider exposes, including nested-block cardinality
(`nesting_mode`/`max_items`). This is precisely the information that,
read by hand, was previously only checked ad hoc (see the "Idiomatic-Go
Cleanup Pass" section above for the `ContainerAppIngress.TrafficWeight`
bug that hand-checking already caught once).

### What was built

`cmd/schema-check` (dev tool, not shipped in the `composey` binary):
shells out to `terraform init` + `terraform providers schema -json` in a
scratch directory declaring every provider composey generates config for
(`hashicorp/aws ~> 5.0`, `hashicorp/azurerm ~> 4.0`, `hashicorp/google
~> 5.0`, `kreuzwerker/docker ~> 3.0`, `hashicorp/random ~> 3.6` — the
exact versions pinned in each cloud's `generator.go`), then reflects over
`models.AWSResources`/`AzureResources`/`GcpResources` and every nested
struct reachable from them, matching each JSON-tagged field against the
schema's `block_types` by name and comparing cardinality: a schema block
with no `max_items` cap needs a Go slice; a struct field can only ever
express one entry.

### What it found

Run for the first time against the real schemas, it found exactly one
real bug beyond what had already been fixed by hand: `azurerm_container_app`'s
`ingress.traffic_weight` has `nesting_mode: "list"` with **no max_items
cap** — genuinely repeatable (Azure supports weighted traffic splits
across multiple revisions, e.g. canary/blue-green deploys), not just
single-item array shorthand. `ContainerAppIngress.TrafficWeight` was a
bare `ContainerAppTrafficWeight` struct, not a slice — composey has
always emitted exactly one 100%-weighted entry, so this produced valid
JSON in practice (Terraform's JSON syntax accepts either shape for what
*looks* like single cardinality), but the Go type couldn't express more
than one revision if that's ever needed, and nothing would have caught
the mismatch except rereading provider docs by hand. Fixed: the field is
now `[]ContainerAppTrafficWeight`; the one call site
(`azure/compute.go`) and all 10 Azure golden fixtures
(`examples/*/expected/azure/main.tf.json`) updated to match (regenerated
via the real compiler pipeline, not hand-edited).

Three other findings were confirmed as *not* bugs, reported only as
informational: `ContainerAppJob.ScheduleTriggerConfig`/`.Template` and
`ManagedRedis.DefaultDatabase` are all Go slices for blocks the schema
caps at exactly one item (`max_items: 1`) — valid, since Terraform's JSON
syntax accepts a one-element array as an alternate spelling of a bare
object for single-cardinality blocks, just not the more minimal encoding.
Zero mismatches on the AWS or GCP side (29 and 12 resource types
respectively) — both were already correct.

### Checkpoint
- ✅ `go run ./cmd/schema-check` reports 0 mismatches across all three
  clouds' resource models
- ✅ Wired into CI (`.github/workflows/ci.yml`) so a future provider
  version bump that changes a block's cardinality fails the build
- ✅ `gofmt -l .` clean, `go vet ./...` clean, `go build ./...` clean,
  `go test ./...` passing (Azure golden fixtures regenerated and
  reverified against the real pipeline, not hand-edited)
- ✅ Documented in `AGENTS.md`'s "Verifying Terraform schema
  compatibility" section

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

## Time Investment

> The paragraph-form recaps that used to follow this table duplicated
> detail already recorded in each phase's own section above; trimmed
> 2026-08-07 to just the table, which stays useful as an at-a-glance
> summary. See each ✅ Phase section for the full account of what
> happened, what broke, and how it was verified.

| Phase | Hours | Status | Description |
|-------|-------|--------|-------------|
| Week 1: Preparation | 10 hrs | ✅ Complete | Setup, testing, harness |
| Week 2: Parser | 15 hrs | ✅ Complete | compose-go integration |
| Week 3: Normalizer | 15 hrs + ~6 hrs hardening | ✅ Complete | Logic port, **Python removed**; three silent bugs found and fixed by a follow-up idiom/integration review, not caught by the original checkpoint's own tests |
| Week 4-5: AWS | 20 hrs budgeted (see Phase 3's scope note); inference+generator+CLI+bridge ported, coverage-gap-surveyed against all 13 relevant Python test files (130 Go tests), cut over as the default `compile_to_terraform` AWS path, then Python AWS backend fully removed (`models/aws.py`, `generator.py`, 6 inference modules, 10 dependent test files) once `compile_application`'s AWS branch was confirmed to have zero live callers | ✅ Complete, Python removed | Inference + generation |
| Week 5-6: Azure/GCP | 15 hrs budgeted; both completed same day as AWS. Azure: models+naming+inference+generator+CLI+bridge ported, 66-scenario coverage-gap-surveyed, cut over, Python removed. GCP: same pipeline ported at explicit lighter-rigor direction (no pre-existing Python test suite to survey against), sanity-checked against 6 hand-run Python outputs rather than an exhaustive golden-example set, cut over, Python removed | ✅ Complete, Python removed (both clouds) | Multi-cloud |
| Week 6: CLI | 10 hrs budgeted; full CLI ported same session as Phase 4 (explain, environment generators for all 3 clouds, main compile command, init command), coverage-gap-surveyed against 79 Python test functions across 3 test files, real bugs found (quoting mismatches) and fixed, then the entire Python package/test suite/pyproject.toml removed once verified redundant across all 13+ examples for all 3 clouds; CI, smoke-test scripts, Makefile, README, and AGENTS.md all updated to match | ✅ Complete, Python fully removed | Standalone Go CLI |
| Idiomatic-Go cleanup + package split | ~unbudgeted, done post-migration (2026-08-07) | ✅ Complete | See the two sections above this one |
| Week 7: Distribution | 10 hrs | ⬜ Pending | Build, release, docs |
| **Total** | **~101 hrs + cleanup** | **Migration 100% complete; distribution (Phase 6) pending** | **~7 weeks part-time** |

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
  same approach the Python generator used (`json.dumps` on nested dicts)
  and the same package the parser/normalizer already depend on. Originally
  planned as `hashicorp/terraform-json` + `hashicorp/hcl/v2`; neither was
  ever installed, and neither actually fits — `terraform-json` parses
  Terraform plan/state output rather than generating config, and `hcl/v2`
  targets HCL syntax, which this project deliberately never emits (JSON
  syntax throughout — "Terraform is a compilation target," not something
  hand-edited). Corrected during Phase 3 scoping (2026-08-06); re-confirmed
  during the idiomatic-Go cleanup pass (2026-08-07) that no such library
  exists to adopt in its place either (see that section above).

**Utilities:**
- gopkg.in/yaml.v3 — transitive only (via compose-go), not a direct
  dependency of composey's own code.
- encoding/json (standard library)

---
