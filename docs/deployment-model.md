# Deployment Model: Identity, Azure Isolation, and CLI Shape

How a deployment is identified and re-addressed across CLI invocations, why
Azure needs its own per-app isolation model, and how the CLI command tree
arrived at its current shape.

## Deployment identity

```text
Environment identity  = backend + environment name
Application identity  = resolved Compose project name
Deployment identity    = environment identity + application identity
                        = cloudcompose/<environment>/apps/<application>.tfstate
```

Generated Terraform directories (`env-<name>/`, `app-<env>-<project>/`)
are compiler scratch space, not identity. Deleting them must never
destroy the ability to reconnect to a deployment, given the same
`environment.yaml` + `compose.yaml` + explicit CLI overrides.

**Litmus test**: given `compose.yaml`, `environment.yaml`, and any
explicit CLI options that are semantically part of the desired
deployment, `cloud-compose` must be able to delete all generated
artifacts, re-run, and address the same deployment. Anything that
breaks this must be either derived deterministically or stored durably
— never left as an ambient side effect of a prior CLI invocation.

### How identity resolves today

- **Application identity** is the top-level `name:` field in
  `compose.yaml`, full stop — no `-p`/`--project` override, no
  `COMPOSE_PROJECT_NAME` environment variable, no directory-basename
  fallback. compose-go's own fuller precedence chain (`cli` package)
  isn't used, since two of its four rungs are exactly the ambient,
  un-derivable, un-persisted inputs the litmus test rules out.
- **Environment identity** is `environment.yaml`'s `name:` field alone,
  with no `--env-name` override — the same shape as application
  identity, deliberately.
- **`--env`/`-e`** (on every command that takes an environment) always
  means the authored `environment.yaml` file. It resolves
  `<dir of environment.yaml>/env-<name>` and reads that directory's
  Terraform outputs — it never creates, regenerates, or applies the
  environment itself; if it hasn't been applied yet, commands fail
  clearly, pointing at `env init`/`env up`. This works identically for
  `local:` and `remote:` backends (see `docs/environment-and-state.md`),
  since resolution never depends on the backend, only on the directory
  already existing with real Terraform outputs in it.
- **`backend:`** is a required field in `environment.yaml` (no more
  implicit "omitted means local state") — see
  `docs/environment-and-state.md` for its `local:`/`remote:`
  shape. State keys are derived deterministically from `name:`, never
  authored: `cloudcompose/<envName>/environment.tfstate`,
  `cloudcompose/<envName>/apps/<project>.tfstate`
  (`internal/compiler/shared/backend_naming.go`).
- **`x-cloud.azure.subnet_index`** (Azure only) is a required top-level
  field in `compose.yaml` — placement is explicit and authored on the
  app, not derived or defaulted, since an unspecified value isn't the
  same as explicitly choosing subnet 0. Two apps sharing an environment
  must not share an index; Azure's own API rejects the resulting
  overlapping subnet ranges at `terraform apply` (`compile` prints a
  note pointing at this whenever compiling for Azure). See "Per-app
  isolation on Azure" below.

### Explicitly rejected alternatives

- **New `EnvironmentState`/`EnvironmentDefinition` domain types.** Not
  needed — `environment.yaml`'s `name` + `backend` already encode this;
  the missing piece was a resolution function, not a new model.
- **A separate CloudCompose environment registry** (`cloudcompose env
  add prod ...`). Sugar at best. `environment.yaml` is already a
  portable handle a human can carry around; `ListDependentApps` already
  answers "what apps exist in environment X" from the backend directly.
- **Terraform workspaces.** The backend key scheme
  (`cloudcompose/<env>/...`) already gives namespace semantics without
  workspaces' ambient-selected-state weirdness.
- **A CloudCompose-managed subnet-index allocator/registry** for Azure.
  New backend read/write plumbing, races between concurrent `compile`
  runs. Hashing app names into the index space was also rejected: a
  real collision risk at realistic scale (5 apps sharing an environment
  is already ~9% likely to collide, 10 apps ~33%, assuming 128 slots).
- **Deriving an app-side local backend path from a local-backend
  environment.** For a real remote backend, an app's state key is
  derived from the environment's own bucket/container plus a different
  key; `local:` has no equivalent, since it names one specific file, not
  a namespace two apps could share slices of. Apps compiled against a
  local-backend environment keep using Terraform's own default local
  state, unrelated to the environment's own authored path.

### History

This model replaced several earlier, weaker mechanisms, in order:

1. **Resolved Compose project identity** — deleted `-p`/`--project` and
   the directory-basename fallback (`compile.go`'s old
   `resolveProjectName`); `ParseCompose` now requires a non-empty
   top-level `name:`. Bundled into the same change: `--subnet-index`'s
   silent default of `0` was removed (a default of `0` is
   indistinguishable from an operator explicitly choosing subnet 0).
2. **`environment.yaml` became directly resolvable** — added
   `resolveEnvironmentByDefinition` (now `environmentDirFromDefinition`),
   reachable at the time via a separate `--environment <file>` flag
   alongside the older `--env <dir>` debug affordance.
3. **Azure's `--subnet-index` flag was removed**, replaced by the
   required `x-cloud.azure.subnet_index` field described below.
4. **`backend:` became mandatory**, with an explicit `local:` value —
   `Backend == nil` used to mean local state silently; now `local:
   {path: ...}` is the explicit, authored choice that omission used to
   stand in for.
5. **`--env`/`--environment` were unified** into a single `--env`/`-e`
   flag meaning the authored file, on every command. The old
   directory-pointing `--env <dir>` was removed entirely, since the
   directory it named was already 100% mechanically derived from
   `environment.yaml`'s `name:`.
6. **`backend:`'s remote shape stopped naming the cloud twice** —
   collapsed AWS/Azure/GCP's separate remote blocks into a single
   `remote:` key, whatever fields it accepts chosen by `provider:`
   rather than by which YAML key was used.
7. **`compile --demo` was removed** — it compiled against a built-in
   synthetic environment (placeholder VPC/ALB/ARN IDs, etc.) instead of
   a real, applied one. Removed for cost of keeping three clouds' worth
   of placeholder shapes in sync; `--env` is now unconditionally
   required to `compile`.
8. **`compose up`/`down`/`ps`/`logs` were flattened back onto the root
   command** — see "CLI shape" below for why the `compose` group was
   removed after being tried.

## Per-app isolation on Azure

### The problem

This codebase's model of "environment" was originally AWS-shaped: on
AWS, the real isolation primitive (the security group) lives *below*
the shared ECS cluster, so sharing that cluster across apps is fine.
Azure has no equivalent lower layer:

- `azurerm_container_app`'s schema has zero networking/security fields.
- Every Container App in one Container Apps Environment shares the same
  subnet and can reach every other Container App in that environment via
  its internal FQDN by default. NSGs apply at the subnet level, not
  per-app.
- Microsoft's own docs: *"A Container Apps environment is a secure
  boundary around one or more container apps and jobs"* and *"Use more
  than one environment when you want two or more applications to...
  never share the same compute resources."*

So on Azure, the Container Apps Environment **is** the isolation
boundary. A shared-CAE-many-apps model means two apps sharing one
environment are not isolated from each other at all.

Managed-service network reachability (Postgres/MySQL Flexible Server,
Managed Redis via delegated subnets/private endpoints) was already
correctly isolated — arguably stronger than AWS's security-group model.
What wasn't isolated: compute-to-compute reachability, and the delegated
subnets themselves (shared across apps, distinguished only by
RBAC/firewall rules, not network segmentation).

### Design

`cloud-compose init` and `cloud-compose compile` split differently on Azure
than on AWS:

- **`init` creates** (the Cloud Compose Environment layer): resource
  group, Log Analytics workspace, VNet, and region/retention/HA/backup
  policy fields.
- **`main` creates** (the Cloud Compose App layer, one per app): its own
  `azurerm_container_app_environment` and its own delegated subnets
  (`infrastructure`/`postgresql`/`mysql`/`redis`), scoped to that one
  app.
- AWS's model is unchanged — its isolation already lives at the
  security-group layer below the shared ECS cluster.

#### Subnet allocation: `x-cloud.azure.subnet_index`

`compile` runs independently per app with no live coordination
mechanism (it only reads the environment's Terraform outputs, not a
registry of claimed subnet ranges). Rather than a CloudCompose-managed
allocator/registry (new backend read/write plumbing, races between
concurrent compiles) or hashing app names into CIDR offsets (real
collision risk at realistic app counts -- see the "Explicitly rejected
alternatives" collision-probability table above), placement is explicit
and authored on the app itself: a required top-level
`x-cloud.azure.subnet_index` in `compose.yaml` (Azure-only; ignored on
AWS/GCP), a small `0`-based integer unique per app within one
environment.

This lives on the app, not the environment: it's app-specific
placement, the same category of decision as `x-cloud`'s per-service
settings, just app-wide instead of per-service (see
`models.AppXCloud`). `compile` fails outright if it's missing when
compiling for Azure -- no default, since an unspecified value isn't the
same as explicitly choosing subnet 0.

Two apps sharing an environment must not share an index. CloudCompose
does not detect this itself; Azure's own API rejects the resulting
overlapping subnet address ranges at `terraform apply` (a real, if
late, backstop already built into the platform). `compile` prints a
note naming the flag whenever compiling for Azure, so that failure mode
is easy to recognise rather than a cryptic Azure API error.

#### CIDR math

`init` reserves the upper half of the VNet CIDR for apps
(`Cidrsubnet(vnetCIDR, 1, 1)`). For the default `10.0.0.0/16`, that's
`10.0.128.0/17`. `main` carves `app_subnet_index`'s own `/24` slice out
of that range (`Cidrsubnet(appsCIDR, 7, app_subnet_index)`), and within
it carves the same 4 subnets `init` used to own, now at `/26` each (64
addresses — double Container Apps' documented `/27` minimum). This
supports up to 128 apps per environment at the default VNet size. Not
configurable — `vnet_cidr` is already the field to widen if more
headroom is needed.

### Status

Implemented (2026-08-11). `cloud-compose init` no longer creates a
Container Apps Environment or subnets; `cloud-compose compile` creates its
own per app. Verified against real Azure (`production-stack`, renamed
to `edge-and-scaling` since): the
per-app Container Apps Environment and all four delegated subnets
created and destroyed cleanly.

### Deferred

**GCP** — Cloud Run's own isolation model hasn't been checked against
this same lens. Not assumed to share Azure's gap; a separate
investigation, consistent with GCP's existing lighter-verification scope
decision (see `docs/compiler-design.md`'s GCP gaps section).

## CLI shape: history and current state

### Current state

The binary is `cloud-compose` (no rename ever happened — a shorter
name was proposed but never checked for namespace availability, so it
stayed a proposal). The CLI tree has two groups:

- `env init` / `env up` / `env down` — the shared-platform commands
  (`env` is a real subcommand group, since it operates on a genuinely
  different target than a single app).
- `up` / `down` / `ps` / `logs` — the single-app commands, top-level on
  the root command.
- `compile` — top-level, unchanged: the explain/no-apply pipeline stage.

### History

This doc originally proposed a `<bin> env <verb>` / `<bin> compose
<verb>` structure, mirroring `docker`/`docker compose`. The `env` half
landed as designed. A `compose` parent (`compose up`/`down`/`ps`/`logs`)
was also implemented for a period, but typing `compose` before every
single-app verb turned out to feel redundant in practice — unlike `env`,
which disambiguates against a real top-level collision risk, `compose`
resolved nothing, since there was never a competing bare `up`/`down`/
`ps`/`logs` to confuse it with. `compose` was removed and those verbs
flattened back onto the root command.

The binary rename itself (`cloudcompose` → a shorter name) never
happened. The original open question — whether a short name like
`cloud` is even available across Homebrew/apt/PyPI/npm — was never
resolved, and the idea was dropped rather than pursued further.
