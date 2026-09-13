# Deployment Identity: Design Notes

## The problem

CloudCompose currently has no first-class notion of "the deployed instance
of environment X" or "the deployed instance of application Y". Both are
identified today by filesystem convention — a generated Terraform
directory sitting next to whatever file produced it — rather than by
anything durable or portable:

- Environment side: `--env <dir>` on `compose up`/`compose down`/`env down`
  means "an existing directory with `main.tf.json` and (usually local)
  Terraform state in it", found by the operator, not derived or recorded.
- Application side: with no `-p` flag, the project name defaults to
  `filepath.Base(filepath.Dir(absCompose))` (`compile.go:277-289`) — the
  compose file's own directory name at the time of compilation. `compose
  down`'s flag help text (`compose_down.go:82`) admits this directly:
  *"Must match whatever `compile` used to produce the app's output
  directory"* — the tool asks the human to remember and re-supply an
  identity it never persisted itself.

Neither generated directory should be part of CloudCompose's domain
model; both are currently filling that role by accident.

## What already exists (don't rebuild this)

- `environment.yaml` can carry a `backend:` block (S3+DynamoDB / azurerm /
  GCS) — `internal/compiler/aws/environment_generator.go:14-18,67-85,332-335`.
- State keys are already derived deterministically from the environment's
  `name:` field, not authored:
  `cloudcompose/<envName>/environment.tfstate`,
  `cloudcompose/<envName>/apps/<project>.tfstate` —
  `internal/compiler/shared/backend_naming.go:33-45`.
- `ListDependentApps` (`backend_listing.go`, per cloud) already lists
  `cloudcompose/<env>/apps/` in the backend and recovers project names
  from the keys — this is a working "what apps exist in this
  environment" registry, derived, not stored separately.
- Local (no-backend) state is an explicit, warned-about choice
  (`initconfig.BackendWarnings`), not a silent default to be papered over.

So the environment side of durable identity is `name + backend`, and it
mostly already exists — it just isn't reachable without first having a
directory. The application side is missing entirely: the compose-spec's
own resolved project name is discarded in favor of a directory basename.

## Identity model

```text
Environment identity  = backend + environment name
Application identity  = resolved Compose project name
Deployment identity    = environment identity + application identity
                       = cloudcompose/<environment>/apps/<application>.tfstate
```

Generated Terraform directories are compiler scratch space, not identity.
Deleting them must never destroy the ability to reconnect to a
deployment, given the same `environment.yaml` + `compose.yaml` +
explicit CLI overrides.

## Litmus test / invariant

> Given `compose.yaml`, `environment.yaml`, and any explicit CLI options
> that are semantically part of the desired deployment, CloudCompose must
> be able to delete all generated artifacts, re-run, and address the same
> deployment.

Anything that breaks this must be either derived deterministically or
stored durably — not left as an ambient side effect of a prior CLI
invocation (e.g. today's `--subnet-index` on Azure).

## Sequenced plan

### 1. Fix resolved Compose project identity (do this first)

Application identity is the top-level `name:` field in `compose.yaml`,
full stop — no `-p`/`--project` override, no `COMPOSE_PROJECT_NAME`
environment variable, no directory-basename fallback. This mirrors the
environment side exactly: environment identity is `environment.yaml`'s
`name:` field alone, with no `--env-name` flag to override it; application
identity should work the same way.

Note that compose-go's own precedence chain (`-p`/`WithName` >
`COMPOSE_PROJECT_NAME` > top-level `name:` > directory basename, with a
logged warning on symlinks) lives in the `cli` package
(`compose-go/v2/cli/options.go:554-572`), which CloudCompose does *not*
currently import — `ParseCompose` calls the lower-level `loader.Load`
directly, which only implements the `name:` YAML-reading step natively.
Deliberately don't pull in the `cli` package's fuller chain here: two of
its four rungs (an env var, a directory basename) are exactly the ambient,
un-derivable, un-persisted inputs the litmus test above rules out. Only
the YAML-native `name:` resolution is used.

```go
// ParseCompose already loads via loader.Load; require a name instead of
// setting a placeholder, and surface it on the returned type.
if project.Name == "" {  // no top-level `name:` in the compose file
    return nil, fmt.Errorf("compose file must set a top-level `name:` (see docs/...)")
}
```

Currently `ParseCompose` loads with a hardcoded placeholder project name
(`parser.go:93-99`), and the real name is computed independently in
`compile.go` via directory basename (`resolveProjectName`,
`compile.go:277-289`) — both are deleted. The `-p`/`--project` flag is
removed from `compile`/`compose up`/`compose down` entirely, not kept as
an override.

**Case considered and rejected:** deploying the same `compose.yaml`
more than once *within a single environment* (e.g. multiple instances of
one codebase) has no name-file-based way to disambiguate once `-p` is
gone. This is a different, currently non-existent use case from what `-p`
is used for today (supplying a name because the file doesn't have one),
and nothing in the codebase or this design needs it yet. If it's ever
needed, it should get its own explicit mechanism (e.g. an "instance"
concept layered on top of application identity) rather than resurrecting
a flag that silently overrides the one name the file declares.
Deploying the same file into *different environments* (e.g. `staging-1`
vs `staging-2`) needs no such mechanism — that's already disambiguated by
the environment half of the deployment-identity key
(`cloudcompose/<env>/apps/<project>.tfstate`).

Once this lands, `compose down`'s "must match whatever compile used"
caveat becomes unnecessary — a symptom of missing identity, not a
reasonable UX contract to keep documenting (`compose_down.go:52-61`
recomputes the same path `compile` used purely because there's no stored
link; it becomes correct-by-construction once there's only one path to
derive a project name).

**Bundled into this same change:** remove `--subnet-index`'s silent
default of `0` (`compile.go:424`). A default of `0` is indistinguishable
from an operator explicitly choosing subnet 0 — it's implicit input
masquerading as a harmless default. Make it required (no default) when
the target is Azure. This does *not* attempt to solve subnet
allocation itself (see item 3 below) — it only stops the CLI from
silently accepting an unspecified value as if it were a real choice.

**Explicitly deferred, not part of this change** (real but orthogonal to
identity; tracked here so they're known-deferred, not missed):

- `resolveComposeFile`'s implicit directory scan when `-f` is omitted
  (`compose_file.go:10-15`, `compose_discovery.go:21-32`) — same
  "guessing from cwd" smell as the project-name fallback, but on the
  input side. Worth a follow-up pass to require `-f` explicitly and
  delete `FindComposeFile`.
- `--env`/`--demo` mutual exclusivity checked at runtime
  (`compile.go:76-82`) instead of declaratively via
  `MarkFlagsMutuallyExclusive` — minor flag hygiene, unrelated to
  identity.
- `initEnvironment`'s pointer-field nil-check-and-default blocks in
  `env_init.go:88-104` (`RetainDataOnDestroy`, `Domain`,
  `HighAvailabilityEnabled`, `BackupRetentionDays`,
  `LogRetentionDays`) — silent defaults baked into the CLI layer rather
  than centralized in `shared/constants.go`. Same "explicit is better
  than implicit" smell, no urgency; pick up opportunistically if
  `env_init.go` is already being touched for item 2 below.

### 2. Make `environment.yaml` directly resolvable (done)

Added `resolveEnvironmentByDefinition(environmentYamlPath)`
(`cmd/cloudcompose/environment_resolve.go`), reachable via a new
`--environment <file>` flag on `compile`/`compose up` (mutually
exclusive with `--env <dir>`/`--demo`). It:

- requires `backend:` to be set (loud error if absent — this path is
  specifically for portable/durable resolution; bare local state remains
  a legitimate, separately-supported single-developer mode via
  `env init`/`env up` as they exist today)
- regenerates the environment's `main.tf.json` on demand, by calling
  `initEnvironment` (the same function `env init`/`env up` already
  use — idempotent, always overwrites)
- runs `terraform init` against the backend key derived from
  `BackendKeyForEnvironment(name)`
- delegates to the existing `LoadEnvironment(dir)` unchanged

No new types were needed (no `EnvironmentDefinition`/`EnvironmentState`
split) — the shape already existed as `environment.yaml`'s `name` +
`backend` fields plus `backend_naming.go`'s deterministic key derivation;
this was purely a resolution function stitching existing pieces
together. `--env <dir>` remains available as the lower-level/debug
affordance it already was.

Not yet extended to `compose down`/`compose ps`/`compose logs`/`env
down` — those still take `--env <dir>` only. Worth a follow-up once
there's a concrete need (e.g. tearing down or inspecting a deployment
without a pre-existing directory), but `compile`/`compose up` are the
primary "deploy an app" path this item exists to fix, so extending the
rest was left out of this change's scope.

### 3. Audit remaining non-deterministic regeneration inputs

Azure's `--subnet-index` (`docs/azure-app-isolation-design.md`) is a
known violation of the litmus test: it's external allocation state
supplied per-invocation, not derivable from `environment.yaml` +
`compose.yaml` alone, and not currently persisted anywhere. Options,
roughly in order of preference:

- Persist the allocation durably (e.g. alongside the app's state key or
  as an environment-level output), so regeneration reads it back rather
  than requiring the flag again.
- Derive it from something already durable — e.g. `ListDependentApps`
  already enumerates every app under an environment; subnet allocation
  could in principle be computed from that enumeration rather than
  hashing the project name (naive hashing reintroduces collision
  handling, and probing-on-collision reintroduces order-dependence
  unless the result of the probe is itself persisted).

Don't hash-and-probe without persisting the result — that just moves the
non-determinism rather than removing it. This is explicitly an allocator
problem, not a naming problem, and shouldn't block (1) or (2).

## Explicitly rejected alternatives

- **New `EnvironmentState`/`EnvironmentDefinition` domain types.** Not
  needed — `environment.yaml`'s `name` + `backend` already encode this;
  the missing piece was a resolution function, not a new model.
- **A separate CloudCompose environment registry (`cloudcompose env add
  prod ...`).** Sugar at best. `environment.yaml` is already a fine
  portable handle a human can carry around; `ListDependentApps` already
  answers "what apps exist in environment X" from the backend directly.
  No registry needed for "what environments exist" either — that's a
  convenience layer that can be added later without changing the
  identity model.
- **Terraform workspaces.** The backend key scheme
  (`cloudcompose/<env>/...`) already gives namespace semantics without
  workspaces' ambient-selected-state weirdness.
