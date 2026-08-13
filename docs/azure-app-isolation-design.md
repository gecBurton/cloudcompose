# Per-app isolation on Azure

## The problem

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

## Design

`cloudcompose init` and `cloudcompose main` split differently on Azure
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

### Subnet allocation: `--subnet-index`

`main` runs independently per app with no live coordination mechanism
(it only reads the environment's Terraform outputs, not a registry of
claimed subnet ranges). Rather than hashing app names into CIDR offsets
(collision risk) or querying Azure directly for a free slot (new API
surface), placement is explicit: a required `--subnet-index` flag
(Azure-only; AWS's `main` ignores it), a small `0`-based integer unique
per app within one environment — the same pattern `-p`/`--project`
already uses for app identity, applied to app *placement*.

### CIDR math

`init` reserves the upper half of the VNet CIDR for apps
(`Cidrsubnet(vnetCIDR, 1, 1)`). For the default `10.0.0.0/16`, that's
`10.0.128.0/17`. `main` carves `app_subnet_index`'s own `/24` slice out
of that range (`Cidrsubnet(appsCIDR, 7, app_subnet_index)`), and within
it carves the same 4 subnets `init` used to own, now at `/26` each (64
addresses — double Container Apps' documented `/27` minimum). This
supports up to 128 apps per environment at the default VNet size. Not
configurable — `vnet_cidr` is already the field to widen if more
headroom is needed.

## Status

Implemented (2026-08-11). `cloudcompose init` no longer creates a
Container Apps Environment or subnets; `cloudcompose main` creates its
own per app. Verified against real Azure (`production-stack`): the
per-app Container Apps Environment and all four delegated subnets
created and destroyed cleanly.

## Deferred

**GCP** — Cloud Run's own isolation model hasn't been checked against
this same lens. Not assumed to share Azure's gap; a separate
investigation, consistent with GCP's existing lighter-verification scope
decision (see `docs/azure-aws-parity-todo.md`).
