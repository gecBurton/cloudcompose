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
│       ├── parser.go              # Stage 1: parse Compose via compose-go
│       ├── normalizer.go          # Stage 2: normalize to semantic model
│       ├── infer_aws.go, infer_azure.go, infer_gcp.go
│       │                          # Stage 3: orchestrate inference per cloud
│       ├── compute_aws.go, managed_aws.go, connectivity_aws.go,
│       │   scheduling_aws.go, edge_aws.go, permissions_aws.go
│       │                          # AWS inference: ECS, RDS/ElastiCache/S3,
│       │                          #   networking, EventBridge, CloudFront/WAF,
│       │                          #   IAM wiring
│       ├── azure_compute.go, azure_managed.go, azure_edge.go,
│       │   azure_naming.go
│       │                          # Azure inference: Container Apps/Jobs,
│       │                          #   Flexible Server/Redis/Storage, Front
│       │                          #   Door, hash-based name truncation
│       ├── infer_gcp.go            # GCP inference: Cloud Run, Cloud SQL,
│       │                          #   Memorystore, Cloud Storage
│       ├── generator_aws.go, generator_azure.go, generator_gcp.go
│       │                          # Stage 4: Terraform JSON generation
│       ├── connections_aws.go      # Connection string resolution
│       ├── explain.go              # Inference reporting (--explain flag)
│       ├── environment.go          # LoadEnvironment target dispatcher
│       ├── environment_aws.go, environment_azure.go, environment_gcp.go
│       │                          # Per-cloud environment YAML loaders
│       ├── environment_generator.go, environment_yaml.go
│       │                          # `composey init`'s platform bootstrap
│       │                          #   Terraform generators
│       ├── constants.go            # Centralized constants
│       ├── errors.go               # ComposeyError
│       └── pyjson.go, pyordered_reflect.go
│                                  # Ordered-JSON rendering: Azure/GCP's
│                                  #   generators have no sort_keys
│                                  #   equivalent, so key order must be
│                                  #   preserved exactly through these
│                                  #   rather than round-tripped through a
│                                  #   plain map
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
go test ./internal/compiler/...

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

### Code Style

- `gofmt` for formatting (`make format`)
- `go vet` before considering anything done (`make vet`)
- Prefer explicit over implicit
- Comments should explain *why*, not restate *what* the code already
  says — especially for anything mirroring specific Python behavior that
  looks like it could be a bug (several genuinely are, ported faithfully
  rather than silently "fixed"; see e.g. `azure_compute.go`'s
  `containerSpecAzure` for one documented example)

### Error Handling

```go
import "github.com/gecburton/composey/internal/compiler"

// For user-facing CLI errors:
err := compiler.NewComposeyError("service X is invalid because ...")

// With additional detail shown beneath the main message:
err := compiler.NewComposeyErrorWithDetails("message", "details")
```

Everywhere else, return plain `error` values with `fmt.Errorf`-style
wrapping; `ComposeyError` exists specifically for the CLI's
show-without-stack-trace behavior, not as a general error type.

### Key Constants

Located in `composey-go/internal/compiler/constants.go`:
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
3. Add inference logic to the appropriate `internal/compiler/*_aws.go`
   file.
4. Add unit tests, including a real-boundary test against an actual
   `examples/` compose file where one exists that exercises it.
5. Consider adding to `examples/` (with a matching
   `expected/main.tf.json`) if this is a materially new scenario.

### Modifying the Semantic Model

1. Update `internal/models/semantic.go`.
2. Update `internal/compiler/normalizer.go` to produce the new structure.
3. Update every `internal/compiler/infer_*.go`/`*_aws.go`/`*_azure.go`
   file that consumes the model.
4. Run tests and update `examples/*/expected/` golden files if the change
   affects generated output.

### Adding a CLI Command

1. Add a new `cobra.Command` in `cmd/composey/` (see `compile.go`/`init.go`
   for the existing pattern).
2. Keep business logic in `internal/compiler/` as plain functions
   returning `(result, error)`, and keep `cmd/composey/`'s command
   handlers thin wrappers that call `os.Exit(1)` on error — this is what
   makes the business logic unit-testable without needing to capture
   `os.Exit` (see `environmentTarget`/`compileTerraform` in `compile.go`
   for the pattern; `init.go`'s inline validation is a deliberate
   exception, not the norm — see `plan.md`'s Phase 5 section).
3. Update README.md with usage.

## Important Notes

- **No Python.** The codebase used to be a Python prototype with an
  incremental Go migration; that migration is complete, and all Python
  source/tests were removed. `plan.md` documents the full migration
  history if you need context on why something is the way it is.
- **Determinism is critical** — output must be byte-identical for the
  same inputs. Go's map iteration order is randomized; anywhere this
  codebase needs a specific order (matching a cloud provider's
  non-alphabetizing JSON serializer, or matching a specific insertion
  sequence), it uses an explicit ordered structure (`PyOrdered` in
  `pyjson.go`) or an explicit sort, never relies on map iteration order
  being stable.
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
