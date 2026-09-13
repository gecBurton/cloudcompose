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
invocation (see item 3 below, formerly `--subnet-index` on Azure).

## Sequenced plan

### 1. Fix resolved Compose project identity (do this first)

Application identity is the top-level `name:` field in `compose.yaml`,
full stop — no `-p`/`--project` override, no `COMPOSE_PROJECT_NAME`
environment variable, no directory-basename fallback. This mirrors the
environment side exactly: environment identity is `environment.yaml`'s
`name:` field alone, with no `--env-name` flag to override it; application
identity should work the same way.

Note that compose-go's own precedence chain (`-p`/`WithName` >
`COMPOSE_PROJECT_NAME` > top-level `name:` > directory basename) lives
in the `cli` package (`compose-go/v2/cli/options.go:554-572`), not the
lower-level `loader.Load` `ParseCompose` calls. Deliberately don't pull
in the `cli` package's fuller chain: two of its four rungs (an env var,
a directory basename) are exactly the ambient, un-derivable, un-persisted
inputs the litmus test above rules out. Only the YAML-native `name:`
resolution is used.

`ParseCompose` no longer loads with a hardcoded placeholder project
name (`parser.go:93-99`); the real name is read from the file itself
and required to be non-empty. `resolveProjectName`'s directory-basename
computation in `compile.go` is deleted, along with the `-p`/`--project`
flag on `compile`/`compose up`/`compose down` -- not kept as an
override.

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
  (`compose_file.go:10-15`) — same "guessing from cwd" smell, on the
  input side. Worth a follow-up to require `-f` explicitly.
- `--env`/`--demo` mutual exclusivity checked at runtime instead of
  declaratively via `MarkFlagsMutuallyExclusive` — minor flag hygiene.
- `initEnvironment`'s pointer-field nil-check-and-default blocks in
  `env_init.go` — silent defaults baked into the CLI layer rather than
  centralized in `shared/constants.go`. No urgency.

### 2. Make `environment.yaml` directly resolvable (done)

Added `resolveEnvironmentByDefinition(environmentYamlPath)`
(`cmd/cloudcompose/environment_resolve.go`), reachable via a new
`--environment <file>` flag on `compile`/`compose up` (mutually
exclusive with `--env <dir>`/`--demo`). It requires `backend:` to be
set, regenerates the environment's `main.tf.json` on demand by calling
`initEnvironment` (the same function `env init`/`env up` already use),
runs `terraform init` against the backend key derived from
`BackendKeyForEnvironment(name)`, then delegates to the existing
`LoadEnvironment(dir)`.

No new types were needed — the shape already existed as
`environment.yaml`'s `name` + `backend` fields plus
`backend_naming.go`'s deterministic key derivation; this was purely a
resolution function stitching existing pieces together. `--env <dir>`
remains available as the lower-level/debug affordance it already was.

Not yet extended to `compose down`/`compose ps`/`compose logs`/`env
down` — those still take `--env <dir>` only. `compile`/`compose up` are
the primary "deploy an app" path this item exists to fix; extending
the rest is a follow-up once there's concrete need.

### 3. Fix Azure subnet-index non-determinism (done)

Azure's `--subnet-index` flag was a known violation of the litmus
test: external allocation state supplied per-invocation, not derivable
from `environment.yaml` + `compose.yaml` alone, and not persisted
anywhere.

Considered and rejected: a CloudCompose-managed allocator that persists
claimed indices in the backend (new read/write plumbing per cloud,
races between concurrent `compile` runs, partial-write cleanup if
`apply` never runs) and hashing project names into the index space
(genuine collision risk at realistic scale -- 5 apps sharing an
environment is already ~9% likely to collide, 10 apps ~33%, assuming
128 slots).

Fix: the index moved onto the app itself, as a required top-level
`x-cloud.azure.subnet_index` in `compose.yaml` (see `models.AppXCloud`,
`docs/azure-app-isolation-design.md`) -- not into `environment.yaml`,
since it's app-specific placement, not environment policy. `--subnet-
index` is removed entirely. Collision detection is left to Azure's own
API rejection of overlapping subnet address ranges at `terraform
apply`, rather than CloudCompose building a registry to catch it
earlier -- `compile` prints a note pointing at the likely cause
whenever compiling for Azure, so that failure is recognisable rather
than a cryptic Azure API error.

### 4. Make `backend:` mandatory, with an explicit `local` value (done)

`Backend == nil` used to mean local state — an omission, not an
authored choice, and the one field in `environment.yaml` where "not
set" silently meant something rather than being an error.

Fix: `models.InitConfig.Backend` is now a required, non-pointer
`BackendConfig` naming exactly one of `local:`/`aws:`/`azure:`/`gcp:`
(a plain discriminated struct, no custom YAML (un)marshalling needed).
`initconfig.Validate` rejects a missing/empty `backend:` outright;
`BackendWarnings` no longer has a "no backend configured" case, since
there's nothing left to omit -- only backend-specific weaknesses (e.g.
AWS with no `dynamodb_table`) still warn. Each cloud's
`environment_generator.go` emits a `terraform { backend "local"
{path = ...} }` block for `local:` (previously nothing at all), and no
`output "backend"` (apps compiled against a local-backend environment
keep using Terraform's own default local state, deliberately not
wired up as part of this item -- see "Explicitly rejected
alternatives" below). `resolveEnvironmentByDefinition`'s check became
"is backend local" instead of "is backend nil" -- it still refuses a
local backend specifically, since even an authored path is still one
file on one filesystem, with no durable locator to resolve
`environment.yaml` alone into across machines/checkouts, even though
`local:` is a perfectly valid choice for `env init`/`env up`.

`backend.local.path` is itself required, with no default, and always
resolved relative to `environment.yaml`'s own directory (never an
absolute path, never relative to the shell's cwd) -- an environment's
state file location was otherwise an ambient side effect of whichever
directory `terraform apply` happened to be run in, not an authored
fact, which is exactly the kind of implicit default this project's
identity model rules out elsewhere. `env_init.go` resolves it to an
absolute path before handing it to the generators, so the emitted
Terraform itself works regardless of which directory `terraform` is
later run from.

Every committed `environment.yaml` (`examples/hello/environment*.yaml`,
`scripts/ci-environment.{aws,azure}.yaml`) now declares `backend:
{local: {path: ...}}` explicitly, since none previously set `backend:`
at all. `docs/authored-environment-config.md`'s "Sharing one
environment across multiple users" section was rewritten around
`backend:` being required.

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
- **Deriving an app-side local backend path from a local-backend
  environment.** Considered as part of item 4, deferred: for a real
  remote backend, an app's state key is derived from the environment's
  own bucket/container plus `BackendKeyForApp` (same bucket, different
  key) -- `local` has no equivalent, since it names one specific file,
  not a namespace two apps could share slices of. Solving this would
  mean inventing a naming scheme for a case (multiple apps, one
  single-developer/evaluation environment, all wanting distinct local
  state files) that hasn't come up as a real need; apps compiled
  against a local-backend environment keep using Terraform's own
  default local state, unrelated to the environment's own authored
  path, exactly as they did before this item.
