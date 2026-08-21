# Azure/AWS Feature Parity

Tracks *feature completeness* relative to AWS. For *deployment
verification* status (does the output actually apply against real
Azure), see `docs/azure-todo.md`.

## Current state

Azure has reached feature parity with AWS on all major fronts:

- **Security**: RBAC role assignments for managed-service access
  (`internal/compiler/azure/permissions.go`), credentials routed through
  Key Vault instead of plaintext env vars, connection-string rendering
  generalized via `shared.ResolveValue`/`shared.URLPattern` (shared with
  AWS, not Azure-specific hardcoding).
- **Compose features**: `secrets:` and `x-cloud` platform `config:` both
  wired through Key Vault secrets; `service.Size` flows into database
  sizing; MariaDB detected alongside MySQL; CPU/Memory autoscaling
  metrics supported, with a default policy when none is declared.
- **Architecture**: WAF/security-policy equivalent for Front Door (rate
  limiting on Standard SKU — no managed-rule support, since that needs
  the Premium SKU; see "Open items" below); network isolation enforced
  per-app via one Container Apps Environment per app (see
  `docs/azure-app-isolation-design.md`); RBAC/identity granting is
  usage-driven (mirrors an env var's actual references), not blanket
  `depends_on:`-driven.
- **Robustness**: shared size→CPU/memory table with AWS
  (`shared.SizeMappings`); Consumption-tier CPU/memory ceiling and
  exact-pair validation (Azure's Container Apps requires matched
  cpu/memory pairs, unlike Fargate); backup/HA settings for both
  Postgres and MySQL Flexible Server; health-check/probe configuration
  for Container Apps and Front Door origins.
- **Testing**: Azure's test suite mirrors AWS's one-file-per-source-file
  convention; golden fixtures exist for every example that's
  implementable on Azure at all.

## Open items

- **No `managed_rule` WAF support on Front Door** — Standard SKU (this
  codebase's default) only supports `custom_rule`; AWS-equivalent
  managed rule sets require the Premium SKU, which would change cost for
  every existing deployment. Deliberate scope decision, not planned
  unless a Premium-tier option is explicitly requested.
- **GCP's network-isolation model has not been investigated** against
  the same lens as Azure's (Cloud Run's VPC connector scoping, ingress
  settings, per-service IAM vs AWS's per-`networks:`-segment security
  groups). Deliberately deferred — GCP has lighter verification overall
  (see below).
- **GCP has no size-ceiling rejection** equivalent to Azure's
  Consumption-tier cap enforcement. Not attempted. (Separately: GCP's
  own size-to-resources table was found, during a later codebase
  review, to have drifted from AWS/Azure's `shared.SizeMappings` the
  same way Azure's once had before this doc's Priority 4 item fixed it
  -- GCP's own table has since been fixed to derive from
  `shared.SizeMappings` too, the same consolidation Azure got; this
  ceiling-rejection gap is what's left, not the table itself.)
- **Backup/HA settings not wired for GCP** — Cloud SQL has its own
  equivalent settings; left for a follow-up.
- **`log_retention_days` placement**: currently a common-envelope field
  applied to AWS and Azure; an early GCP spike recommended keeping it
  AWS-only. Not reconciled.

## Explicitly not a gap (intentional architectural differences)

- Azure's shared-server-per-engine database topology vs AWS's
  dedicated-instance-per-service — a real capability tradeoff (shared
  engines can't get independent HA/sizing/maintenance windows), not an
  oversight. See `azure/managed.go`'s `largestServiceSize`.
- Azure's one-shared-ACR-per-environment vs AWS's
  one-ECR-repo-per-service — both valid.
- No shared ALB/ingress equivalent for Azure's `cloud-compose init` —
  Container Apps' per-service-FQDN model has no shared listener to
  configure.
- Rate-schedule rejection when a value doesn't divide evenly into cron —
  a real expressiveness gap vs AWS's native `rate(...)`, handled as a
  clear compile-time error per this project's "reject what you can't
  express" principle.
- Retries/timeouts unmodeled for scheduled jobs — absent on both clouds
  equally.

## GCP scope

GCP intentionally has lighter verification than AWS/Azure — it has never
been tested against a real deployment. This is an accepted scope
decision, not a gap that crept in unnoticed. See `internal/compiler/gcp/`
and `docs/spikes/gcp/README.md` for its own open items (CDN/domain
inference is a documented no-op in `gcp/infer.go`; connection-string
handling for Cloud SQL's unix-socket path vs host:port is unconfirmed).
