# Composey: Project History

Composey was originally a Python prototype; it was migrated to Go over
roughly seven weeks (~101 hours part-time) and is now Go-only, with no
Python source, tests, or runtime dependency remaining anywhere in the
repo. This doc is a short summary of that migration and the notable
engineering decisions since, kept only because a handful of comments in
the current codebase point back to specific facts here. It is not a task
tracker — see `docs/azure-aws-parity-todo.md` for the actively
maintained backlog of open work.

## The migration (2026-07 – 2026-08-06)

Ported incrementally, replacing each Python stage with its Go
equivalent immediately once verified, rather than running both in
parallel for an extended period:

1. **Parser** (compose-go-based, replacing a Docker-CLI-dependent
   Python parser) and **semantic model/normalizer** — ported and
   hardened. A post-checkpoint review found and fixed three real bugs
   the original "tests pass" checkpoint had missed entirely: named-volume
   rejection was silently broken for every real compose file (the test
   suite only exercised a hand-built lookalike type, never the real
   parser boundary), output order was nondeterministic across runs, and
   a validated `min_scale: 0` was silently overwritten to `1`. The
   lesson generalized to every later phase: passing tests copied
   alongside a port prove the port matches the tests, not that either
   matches production — each later phase's own verification was
   deliberately designed around that lesson, not just "does it build."
2. **AWS inference + generation** — ported, coverage-gap-surveyed
   against all 13 relevant Python test files, cut over, then the Python
   AWS backend fully removed once confirmed to have zero live callers.
3. **Azure inference + generation** — same rigor as AWS: ported,
   66-scenario coverage-gap-surveyed, cut over, Python removed.
4. **GCP inference + generation** — ported the same day as Azure, at
   **explicit deliberate direction to move fast and accept lower rigor**:
   GCP had never been tested against a real deployment, and no
   pre-existing Python test suite existed to survey coverage against (so
   there was nothing to survey). Sanity-checked against a handful of
   hand-run Python outputs instead of an exhaustive golden-example set.
   This is why GCP's test coverage and deployment verification remain
   intentionally lighter than AWS/Azure's today — a scope decision made
   once, not a gap that crept in unnoticed.
5. **Standalone Go CLI** — the full CLI (`explain`, per-cloud environment
   generators, `main`, `init`) ported the same session as Azure/GCP,
   coverage-gap-surveyed against 79 Python test functions, then the
   entire Python package/test suite/`pyproject.toml` removed once
   verified redundant across every example for all three clouds.
   `init.go`'s inline provider-validation/tags-parsing logic was left
   calling `os.Exit` directly rather than refactored into pure functions
   like `compile.go`'s `environmentTarget`/`compileTerraform` — a
   deliberate choice given the low complexity of that specific glue
   code, not an oversight.

Verified throughout via real end-to-end CLI runs (not just `go test`)
and byte-identical comparison against golden Terraform fixtures, not
just "the Go tests pass."

## Post-migration engineering passes (2026-08-07 – 2026-08-09)

- **Idiomatic-Go cleanup**: removed Python-port residue (custom
  ordered-map JSON marshalling that existed only to byte-match Python's
  dict insertion order, no longer needed once there was no Python
  output to match), replaced `any`/`map[string]any` escape hatches in
  the Azure/GCP models with real typed structs. Found and fixed two real
  Terraform schema-shape bugs along the way.
- **Package split**: `internal/compiler` (one flat package, ~30 files,
  AWS/Azure/GCP logic all mixed together) split into
  `internal/compiler/{shared,aws,azure,gcp}`, resolving the resulting
  import cycle with a dependency-free `shared` leaf package.
- **`cmd/schema-check`**: a dev tool that cross-checks the hand-written
  Go structs in `internal/models` against the real Terraform provider
  schema (`terraform providers schema -json`), catching cases where a
  nested block's cardinality (single object vs. repeatable list)
  disagrees with what the struct's shape can express. Run in CI.
- **Authored environment config**: `composey init` now reads an
  authored, versioned `environment.yaml` (the decisions — region, VPC
  CIDR, etc.) instead of flags-only; `composey main` reads an
  environment's facts live via `terraform output -json` rather than
  through a generated file, removing an entire class of "is this file
  stale" problem.
- **AWS/Azure feature parity**: a systematic gap analysis
  (`docs/azure-aws-parity-todo.md`) found Azure had no RBAC/Key
  Vault-backed secrets (credentials flowed as plaintext), no compose
  `secrets:`/platform `config:` support, no database sizing, and a
  real connection-string rendering bug. All of Priority 1 (security) and
  Priority 2 (missing features) were closed, along with Redis private
  networking and a database size-table drift bug (Azure's own table had
  silently diverged from AWS's). See that doc for what's still open —
  it is the live tracker, not this file.

## Distribution (not started)

The one part of the original migration plan not yet done: cross-platform
binaries, a GitHub release pipeline, a Homebrew formula, and a
`curl | bash` install script. No work has started on this.
