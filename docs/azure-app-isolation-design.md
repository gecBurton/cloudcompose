# Design: Per-app isolation on Azure (Cloud Compose Environment vs. Cloud Compose App)

> **Status: implemented (2026-08-11).** Landed exactly as decided below,
> in one pass, not staged. `cloudcompose init` no longer creates a
> Container Apps Environment or subnets; `cloudcompose main` creates its
> own per app, carved from the environment's `apps_cidr` at
> `--subnet-index`. All 12 Azure golden fixtures regenerated and
> `terraform validate`d against the real `azurerm` provider. Found and
> fixed a real bug in `shared.Cidrsubnet` along the way: it had no
> bounds checking on `netnum`, so an out-of-range `--subnet-index` would
> have silently computed a CIDR outside the intended range rather than
> erroring -- confirmed against Terraform's own `cidrsubnet` semantics
> and fixed to match.

## The problem

`docs/azure-aws-parity-todo.md` originally framed this as "Azure has no
equivalent of AWS's per-`networks:`-segment security groups" — a wiring
gap. Investigating it surfaced something bigger: the real problem isn't
that Azure lacks a *mechanism*, it's that this codebase's own model of
"environment" is unintentionally AWS-shaped in a way that doesn't map
onto Azure's actual isolation boundary.

## Two concepts this codebase currently conflates under "environment"

**Cloud Compose Environment** — a policy/governance boundary. "production",
"datascience sandbox". What belongs here: log retention days, backup
retention, HA policy, region(s), tagging conventions, who can deploy into
it.

**Cloud Compose App** — a deployment unit. "expense-tracker",
"customer-gateway". Each app should have its own Postgres, Redis,
storage, and compute, isolated from every other app's. Isolation between
apps is a real requirement, not a nice-to-have.

Today, `environment.yaml`/`cloudcompose init` creates one shared thing per
cloud, and every `cloudcompose main` run (one per app) deploys into it.
On AWS this happens to work, because AWS's real isolation primitive
(the security group) lives *below* the shared thing (the ECS cluster) —
the cluster is a scheduling convenience with no security meaning, and
security groups are already applied per-service, per-`networks:`-segment,
completely independent of which cluster a task runs in.

Azure has no equivalent lower layer. Confirmed directly (not assumed):

- `azurerm_container_app`'s schema has **zero** networking/security
  fields at all (`terraform providers schema -json`, checked against
  the real 4.x provider — `block_types`: `dapr`, `identity`, `ingress`,
  `registry`, `secret`, `template`, `timeouts`; no network-related
  attribute or block anywhere).
- Microsoft's own networking docs: every Container App in one
  environment shares the same subnet and can reach every other
  Container App in that environment via its internal FQDN by default.
  NSGs apply at the subnet level, not per-app.
- Microsoft's own environment docs, verbatim: *"A Container Apps
  environment is a secure boundary around one or more container apps
  and jobs"* and *"Use more than one environment when you want two or
  more applications to: Never share the same compute resources... Be
  isolated due to team or environment usage."*

So on Azure, the Container Apps Environment **is** the isolation
boundary — there is nothing below it to attach isolation to instead.
Today's one-shared-CAE-many-apps model means two apps sharing one Cloud
Compose Environment are not actually isolated from each other on Azure
at all, regardless of anything this project does at the compute-manifest
level.

## What's already correctly isolated, and what isn't

**Already isolated, no change needed:** managed-service network
reachability. Postgres/MySQL Flexible Server and Managed Redis already
get delegated subnets / private endpoints — genuinely unreachable from
outside the VNet at all. This is arguably a *stronger* guarantee than
AWS's security-group model gives for the equivalent RDS/ElastiCache
resources (which are reachable by anything holding the right security
group, not blocked at the network layer entirely).

**Not isolated today:** compute-to-compute. Every Container App in the
shared CAE can reach every other Container App in the same CAE over its
internal FQDN — including two unrelated apps sharing one Cloud Compose
Environment.

**Also not isolated today, found while researching this:** the delegated
subnets themselves. `azurerm_subnet.postgresql`/`mysql`/`redis` are each
created *once* per environment by `cloudcompose init`, and every app's
Postgres/Redis lands in the same shared subnet — apps are distinguished
only by RBAC/firewall rules at the resource level, not by network
segmentation. If per-app isolation is the goal, this needs to move too,
not just the CAE.

## Decision

Redefine the Azure `init`/`main` split so `main` (not `init`) creates the
per-app Container Apps Environment, inside the shared VNet `init`
creates. Concretely:

- **`cloudcompose init` keeps creating** (the Cloud Compose Environment
  layer): resource group, Log Analytics workspace, VNet, and the
  region/retention/HA/backup policy fields already on `AzureEnvironment`.
- **`cloudcompose main` now creates** (the Cloud Compose App layer, one
  per app): its own `azurerm_container_app_environment`, its own
  delegated subnets (`infrastructure`/`postgresql`/`mysql`/`redis`, all
  scoped to this one app, not shared), and everything already inferred
  per-app today (Postgres, Redis, Storage, Container Apps themselves).
- AWS's model is **unchanged** — its isolation already lives at the
  security-group layer below the shared ECS cluster, so nothing here
  applies to it. The two clouds' `environment.yaml`/`init`/`main` split
  will do genuinely different things under one shared authored schema
  after this — worth being explicit about that asymmetry in the docs
  rather than pretending the two clouds are structurally identical.

## Decided: per-app subnet allocation with no coordination

`cloudcompose main` runs independently per app, with no mechanism to
coordinate with other apps' deployments into the same environment — it
only reads the environment's own Terraform outputs (`terraform output
-json`), not a live registry of "which subnet ranges are already
claimed." Terraform itself has no "claim a free slot" primitive.
Considered and rejected:

- **Hash the app name into a CIDR offset** — collisions become a real
  (rare but real) failure mode with no clean resolution path.
- **Have `main` query Azure directly to find a free subnet slot** — a
  new capability (`main` would need live Azure API access beyond
  reading Terraform outputs) this tool doesn't have today, and a
  material scope increase on its own.

**Decided**: an explicit, authored `app_subnet_index` — a new field
supplied when invoking `cloudcompose main` (see "Where this field lives"
below), analogous to how the person deploying multiple apps into one
environment is already responsible for giving each a distinct app
name. `main` derives the actual CIDR deterministically from
`app_subnet_index` plus a CIDR range `init` reserves for apps (see
"CIDR math" below). Two apps sharing an index would collide — the same
class of user error as two apps sharing a name, not a new failure mode
this design introduces.

## Decided: where `app_subnet_index` lives

Not `environment.yaml`: it's a per-app decision, and `environment.yaml`
is authored once per Cloud Compose Environment, not once per app.
`cloudcompose main` has no per-app authored config file today (unlike
`init`'s `environment.yaml`) — its only per-app inputs are CLI flags
(`-f`, `-e`, `-p`/`--project`, `-o`).

**Decided**: a new required flag, `--subnet-index` (Azure-only; AWS's
`main` ignores it, since AWS needs no per-app subnet), mirroring
`-p`/`--project`'s existing shape — a small integer, `0`-based, unique
per app within one Cloud Compose Environment. No mapping file, no
derivation from the project name: explicit and simple, matching how
`--project` itself is already just typed by the person deploying, not
computed. Rejected a name-derived mapping file as unnecessary
indirection for a problem `--project` already solves the same way for
app *identity* — app *placement* can use the identical pattern.

## Decided: CIDR math for the reserved app range

Today, `init`'s VNet is carved into exactly 4 fixed subnets by index
(`shared.Cidrsubnet(vnetCIDR, 5, 0..3)` for
infrastructure/postgresql/mysql/redis). Under this design, `init`
instead reserves one larger range for "apps," and each app's `main` run
carves *its own* 4 subnets out of its own slice of that range, keyed by
`app_subnet_index`.

**Decided**: `init` reserves the upper half of the VNet CIDR for apps
(`Cidrsubnet(vnetCIDR, 1, 1)` — the existing helper, one call, no new
math needed at that layer). For the default `10.0.0.0/16` every current
example uses, that's `10.0.128.0/17` (32,768 addresses). `main` then
carves `app_subnet_index`'s own `/24` slice out of that range
(`Cidrsubnet(appsCIDR, 7, app_subnet_index)`), and within its own `/24`
carves the same 4 subnets `init` used to own, now at `/26` each (64
addresses — double Container Apps' own documented `/27` minimum for
workload-profile environments, confirmed against Microsoft's own
networking docs, not assumed).

This supports up to **128 apps per Cloud Compose Environment**
(32,768 ÷ 1,024 per app) at the default VNet size, comfortably above any
realistic number of apps sharing one environment, with headroom to
spare on each individual subnet's own minimum. Not made configurable:
a fixed convention, re-derivable from `vnet_cidr` alone with no new
`environment.yaml` field — if an environment genuinely needs more than
128 apps or larger per-app subnets, `vnet_cidr` itself is already
the field to widen, exactly as it is today.

## Scope and blast radius

Not a small change, but no live deployments or golden-fixture
compatibility to protect — every existing Azure golden fixture assumes
the current shared-CAE model
(`data.azurerm_container_app_environment.main` read as a data source in
`main`'s own output) and will need regenerating regardless of how this
is sequenced, not just ones exercising network isolation specifically.
`AzureEnvironment`'s existing `PostgresqlSubnetID`/`MysqlSubnetID`/
`RedisSubnetID`/`InfrastructureSubnetID`-shaped fields (read from the
environment by `main` today) become something `main` itself
computes/creates rather than reads from the environment, touching
`internal/models/azure.go`, `azure/environment.go`'s decode side,
`azure/environment_generator.go`, `azure/infer.go`, and every example's
expected fixture.

**Decided: one change, not staged incrementally.** Nothing to migrate
means no reason to ship a half-state (e.g. `main` creating its own CAE
while still reading a hardcoded subnet index) that would itself need a
second pass to finish. Land the full redesign in one pass: `init`'s
VNet/apps-range split, `main`'s own CAE + subnet creation, the
`--subnet-index` flag, and every fixture regenerated together.

## Deferred, not part of this decision

- **GCP.** Cloud Run's own isolation model hasn't been checked against
  this same lens — a genuinely separate investigation, not assumed to
  have the same shape as Azure's gap just because it's a third cloud.
  GCP already has deliberately lighter verification than AWS/Azure
  (never tested against a real deployment) and a lighter-weight
  network-isolation story is consistent with that existing, documented
  scope decision — not something this doc is extending to cover.
