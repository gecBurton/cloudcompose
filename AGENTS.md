# Composey: Agent Development Guide

## Project Overview

Composey is a Docker Compose to Terraform compiler that provides a
PaaS-like deployment experience for AWS, Azure, and GCP. It transforms
annotated Docker Compose files into cloud infrastructure using
intent-based abstractions.

The compiler is implemented entirely in Go, in `composey-go/`. There is
no Python runtime dependency: the codebase migrated incrementally from a
Python prototype (see `plan.md` for that history) and the last Python
source was removed once the Go implementation was verified
byte-identical against it for every supported cloud.

**Architecture**: 4-stage compiler pipeline
1. **Parse**: Load Compose files via the `compose-go` library
2. **Normalize**: Transform Compose into a cloud-agnostic semantic model
3. **Infer**: Map semantic intent + environment context to cloud resources
4. **Generate**: Produce deterministic, canonical Terraform JSON

**Supported Clouds**:
- **AWS**: ECS Fargate, RDS, ElastiCache, S3, ALB, CloudFront
- **Azure**: Container Apps, PostgreSQL/MySQL Flexible Server, Managed
  Redis, Blob Storage, Key Vault, Front Door
- **GCP**: Cloud Run, Cloud SQL, Memorystore, Cloud Storage

## Quick Start

```bash
cd composey-go

# Build the binary
go build -o composey ./cmd/composey

# Run the compiler
./composey main -f ../examples/flask/compose.yml -e ../examples/prod-env.yaml

# Run tests
go test ./...

# Format and vet
gofmt -w . && go vet ./...
```

Or via the `Makefile` at the repo root (`make build`, `make test`,
`make format`, `make vet`).

## Project Structure

```
composey-go/
├── cmd/composey/               # CLI entry point (cobra)
│   ├── main.go                 # root command, parse-only/normalize-only
│   │                            #   subcommands, version
│   ├── compile.go               # `main` command: parse -> normalize ->
│   │                            #   explain/compile -> write, build-context
│   │                            #   copying
│   └── init.go                  # `init` command: shared platform
│                                  #   infrastructure bootstrap
├── cmd/schema-check/            # Dev tool (not shipped): cross-checks
│                                  #   internal/models's structs against
│                                  #   the real Terraform provider schema
│                                  #   (see "Verifying Terraform schema
│                                  #   compatibility" below)
├── internal/
│   ├── models/
│   │   ├── compose.go            # Docker Compose models
│   │   ├── semantic.go           # Cloud-agnostic semantic model
│   │   ├── environment.go        # AwsEnvironment/AzureEnvironment/GcpEnvironment
│   │   ├── aws.go                # AWS resource models
│   │   ├── azure.go              # Azure resource models
│   │   ├── gcp.go                # GCP resource models
│   │   ├── terraform.go          # Terraform manifest model
│   │   └── terraform_common.go   # Cloud-agnostic resources (Docker
│   │                               #   build/push, random_password) shared
│   │                               #   across AWS/Azure
│   └── compiler/
│       ├── parser.go              # Re-exports shared.ParseCompose (thin
│       │                          #   wrappers so cmd/composey's import
│       │                          #   shape didn't need to change)
│       ├── explain.go             # Inference reporting (--explain flag),
│       │                          #   cloud-agnostic
│       ├── environment.go         # LoadEnvironment target dispatcher;
│       │                          #   imports aws/azure/gcp below to
│       │                          #   dispatch on declared `target:`
│       ├── shared/                # Leaf package: no dependency on any
│       │   │                      #   cloud or on the orchestration root
│       │   │                      #   above. Everything genuinely
│       │   │                      #   cloud-agnostic lives here so
│       │   │                      #   aws/azure/gcp can share it without
│       │   │                      #   an import cycle back to the root.
│       │   ├── parser.go           # Stage 1: parse Compose via compose-go
│       │   ├── normalizer.go       # Stage 2: normalize to semantic model
│       │   ├── constants.go        # Centralized cloud-agnostic constants
│       │   ├── errors.go           # ComposeyError
│       │   ├── terraform_json.go   # Shared Terraform-JSON marshalling
│       │   │                       #   helpers used by every generator
│       │   ├── environment_helpers.go
│       │   │                       # CIDR/tag helpers for `composey init`'s
│       │   │                       #   per-cloud platform generators
│       │   ├── sorted_keys.go, url_pattern.go, schedule.go
│       │                           # Small cloud-agnostic helpers used by
│       │                           #   more than one cloud package
│       ├── aws/                   # AWS inference + generation
│       │   ├── infer.go            # Stage 3 orchestrator (InferAWS)
│       │   ├── compute.go, managed.go, connectivity.go, scheduling.go,
│       │   │   edge.go, permissions.go, connections.go
│       │   │                       # ECS, RDS/ElastiCache/S3, networking,
│       │   │                       #   EventBridge, CloudFront/WAF, IAM
│       │   │                       #   wiring, connection strings
│       │   ├── generator.go        # Stage 4: Terraform JSON generation
│       │   ├── iam_policy.go       # AWS-only IAM policy document types
│       │   ├── environment.go, environment_yaml.go
│       │   │                       # AWS environment YAML loader/writer
│       │   └── environment_generator.go
│       │                           # `composey init`'s AWS platform
│       │                           #   bootstrap Terraform generator
│       ├── azure/                 # Azure inference + generation
│       │   ├── infer.go            # Stage 3 orchestrator (InferAzure)
│       │   ├── compute.go, managed.go, edge.go, naming.go
│       │   │                       # Container Apps/Jobs, Flexible
│       │   │                       #   Server/Redis/Storage, Front Door,
│       │   │                       #   hash-based name truncation
│       │   ├── generator.go        # Stage 4: Terraform JSON generation
│       │   ├── environment.go      # Azure environment YAML loader
│       │   └── environment_generator.go
│       │                           # `composey init`'s Azure platform
│       │                           #   bootstrap Terraform generator
│       └── gcp/                   # GCP inference + generation
│           ├── infer.go            # Stage 3 orchestrator (InferGcp) +
│           │                       #   Stage 4 generator (GenerateGcp):
│           │                       #   Cloud Run, Cloud SQL, Memorystore,
│           │                       #   Cloud Storage
│           ├── generator.go        # Stage 4: Terraform JSON generation
│           ├── environment.go      # GCP environment YAML loader
│           └── environment_generator.go
│                                   # `composey init`'s GCP platform
│                                   #   bootstrap Terraform generator
└── go.mod
```

## Development Guidelines

### Adding New Features

1. **Models First**: Define structs in the appropriate `internal/models/`
   file, with JSON tags matching the literal Terraform attribute names.
2. **Add Tests**: Unit tests for logic; real-boundary tests that go
   through `ParseCompose` → `Normalize` → `Infer*` against an actual
   compose file in `examples/`, not just hand-built structs — several
   real bugs during this codebase's Python-to-Go port were only caught
   by a real compose file having the right shape to expose them.
3. **Update Constants**: Add magic strings/values to `constants.go`.
4. **Use `ComposeyError`**: For user-facing errors in the CLI layer
   (`cmd/composey/`), not `internal/compiler`, which returns plain `error`.
5. **Check determinism**: Any new map-shaped or ordered output should get
   a determinism check (run the same input several times, diff the
   output) as it's written, not discovered as a bug later — Go's map
   iteration order is not stable, and this project has hit that class of
   bug more than once.

### Testing

```bash
cd composey-go

# Run all tests
go test ./...

# Run a specific package
go test ./internal/compiler/aws/...

# Verbose output
go test ./... -v

# Vet
go vet ./...
```

There are no golden-file fixtures checked in separately from
`examples/*/expected/`; those are the actual regression tests for AWS and
Azure inference (`TestInferAWS_GoldenExamplesByteIdentical`,
`TestInferAzure_GoldenExamplesByteIdentical`). GCP has no committed golden
files (see `plan.md`'s Phase 4 section for why); its own tests pin
individual outputs directly or check structural validity instead.

### Verifying Terraform schema compatibility

`internal/models/{aws,azure,gcp}.go` are hand-written Go structs, not
generated from anything (see "Terraform is a compilation target" below
for why no codegen/SDK approach was adopted). The risk this creates:
Terraform's JSON syntax accepts a bare object as shorthand for a
single-element list only when a nested block's schema says
`nesting_mode` is `"single"`, or `"list"`/`"set"` with `max_items <= 1`.
A block that's genuinely repeatable (no `max_items` cap) needs a Go
slice; a bare struct field can only ever express one entry, which is
silently wrong rather than a compile error — this is exactly the bug
`cmd/schema-check` found in `ContainerAppIngress.TrafficWeight`.

```bash
cd composey-go
go run ./cmd/schema-check
```

This shells out to `terraform providers schema -json` for every provider
composey generates config for, at the exact versions pinned in each
cloud's `generator.go`, and reflects over the models package's
`*Resources` structs to flag any nested block whose Go shape (slice vs.
non-slice) disagrees with the schema's cardinality. It's run in CI
(`.github/workflows/ci.yml`) so a provider version bump that changes a
block's cardinality fails the build rather than shipping silently wrong
JSON. Requires network access and the `terraform` CLI; not part of the
shipped `composey` binary.

### Code Style

- `gofmt` for formatting (`make format`)
- `go vet` before considering anything done (`make vet`)
- Prefer explicit over implicit
- Comments should explain *why*, not restate *what* the code already
  says — especially for anything mirroring specific Python behavior that
  looks like it could be a bug (several genuinely are, ported faithfully
  rather than silently "fixed"; see e.g. `azure/compute.go`'s
  `containerSpecAzure` for one documented example)

### Error Handling

```go
import "github.com/gecburton/composey/internal/compiler/shared"

// For user-facing CLI errors:
err := shared.NewComposeyError("service X is invalid because ...")

// With additional detail shown beneath the main message:
err := shared.NewComposeyErrorWithDetails("message", "details")
```

Everywhere else, return plain `error` values with `fmt.Errorf`-style
wrapping; `ComposeyError` exists specifically for the CLI's
show-without-stack-trace behavior, not as a general error type.

### Key Constants

Located in `composey-go/internal/compiler/shared/constants.go` (only the
constants genuinely shared across clouds live here; AWS-only constants
like `SizeMappings`/`DBInstanceClasses` are still centralized in this same
file for now rather than split further — see the package-split section
of `plan.md` for why):
- `DatabaseDefaultUsername` — Default DB username
- `SecretsPlaceholderValue` — Placeholder for unset secrets
- `DefaultPortForDatabase`/`DefaultPortRedis`/etc. — Default ports for
  managed services
- `SizeMappings` — Compute size configurations

## Common Tasks

### Adding a New AWS Resource

1. Add a struct to `internal/models/aws.go` with JSON tags matching the
   Terraform attribute names exactly.
2. Add a field to `AWSResources` and its `NewAWSResources()` initializer.
3. Add inference logic to the appropriate `internal/compiler/aws/*.go`
   file.
4. Add unit tests, including a real-boundary test against an actual
   `examples/` compose file where one exists that exercises it.
5. Consider adding to `examples/` (with a matching
   `expected/main.tf.json`) if this is a materially new scenario.

### Modifying the Semantic Model

1. Update `internal/models/semantic.go`.
2. Update `internal/compiler/shared/normalizer.go` to produce the new
   structure.
3. Update every `internal/compiler/{aws,azure,gcp}/infer.go` (and any
   other file in those packages) that consumes the model.
4. Run tests and update `examples/*/expected/` golden files if the change
   affects generated output.

### Adding a CLI Command

1. Add a new `cobra.Command` in `cmd/composey/` (see `compile.go`/`init.go`
   for the existing pattern).
2. Keep business logic in `internal/compiler/` (or its `shared`/`aws`/
   `azure`/`gcp` sub-packages) as plain functions returning
   `(result, error)`, and keep `cmd/composey/`'s command handlers thin
   wrappers that call `os.Exit(1)` on error — this is what makes the
   business logic unit-testable without needing to capture `os.Exit`
   (see `environmentTarget`/`compileTerraform` in `compile.go` for the
   pattern; `init.go`'s inline validation is a deliberate exception, not
   the norm — see `plan.md`'s Phase 5 section).
3. Update README.md with usage.

## Important Notes

- **No Python.** The codebase used to be a Python prototype with an
  incremental Go migration; that migration is complete, and all Python
  source/tests were removed. `plan.md` documents the full migration
  history if you need context on why something is the way it is.
- **Determinism is critical** — output must be repeatable for the same
  inputs (though it no longer needs to be byte-identical to any specific
  historical Python output — that constraint was retired once the Python
  implementation was fully removed). Go's map iteration order is
  randomized, but `encoding/json.Marshal` always sorts map keys
  alphabetically before writing JSON, which is what every generator in
  this codebase relies on for deterministic key ordering — no custom
  ordered-JSON type is needed or used. Anywhere else this codebase
  produces a slice from a map (e.g. security group names, connection
  keys), it sorts explicitly rather than relying on map iteration order.
- **No Silent Failures** — Unknown keys in `x-composey` are errors, not
  ignored (see `models/compose.go`'s `XComposey.UnmarshalJSON`).
- **AWS-First but Cloud-Agnostic** — Semantic model designed for
  multi-cloud; AWS was ported first and most exhaustively verified, Azure
  second with matching rigor, GCP last with deliberately lighter
  verification (see `plan.md`'s Phase 4 GCP section for why).
- **Terraform is a compilation target** — Never hand-edit generated
  output.
- **Ported-not-fixed bugs exist deliberately.** Several places in this
  codebase replicate a Python behavior that reads like a bug (e.g.
  Azure's `_container_spec` rendering `"None"` literally into a URL for
  an unset connection field) rather than silently correcting it during
  the port. These are commented explicitly where they occur. Don't "fix"
  one without checking whether it's an intentional faithful port first.

## Debugging

```bash
cd composey-go

# Show inference decisions without compiling
./composey main -f compose.yml --explain

# Debug with a full Go panic/stack trace on error
COMPOSEY_DEBUG=1 ./composey main -f compose.yml -e env.yaml
```
