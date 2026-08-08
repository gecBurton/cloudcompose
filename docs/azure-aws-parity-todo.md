# Closing the Azure/AWS Feature Gap

> Analysis performed 2026-08-08, evidence-grounded in the current
> codebase (file:line citations throughout). Supersedes nothing —
> `docs/azure-todo.md` tracks *deployment verification* status (does the
> Azure backend's output actually apply against real Azure); this doc
> tracks *feature completeness* relative to AWS. Related open items
> already tracked elsewhere (`docs/spikes/azure/README.md`'s
> `Relationship` enforcement and size-ceiling findings, `plan.md`'s GCP
> lighter-verification decision) are cross-referenced, not duplicated.

## Why this exists

AWS was ported first and most rigorously; Azure got the same review
discipline for its initial port but has visibly fewer capabilities today.
This is the first systematic, code-read (not guessed) comparison of
where the two diverge, prioritized by actual impact rather than by
which file happens to be smaller.

## Method

For each of AWS's `internal/compiler/aws/*.go` files, read the Azure
equivalent (or confirmed absence of one) and recorded concrete
differences with file:line citations. Cross-referenced against
`docs/spikes/azure/README.md` (design-time predictions) and
`docs/azure-todo.md` (deployment-verification status) to avoid treating
an already-tracked item as new.

---

## Priority 1 — Security gaps (no least-privilege, credentials in plaintext)

These are the most serious: AWS enforces least-privilege IAM scoping and
routes secrets through Secrets Manager; Azure does neither for
managed-service access.

- [ ] **Implement RBAC role assignments for managed-service access.**
  `models.RoleAssignment` (`internal/models/azure.go:307`) exists,
  is initialized in `NewAzureResources()` (azure.go:512), and is **never
  written to by any inference code** (`grep -rn "RoleAssignment\[" internal/compiler/`
  returns zero matches). `managedIdentityAzure`
  (`internal/compiler/azure/compute.go:253-261`) only selects identity
  type (system- vs user-assigned) and grants zero permissions. Needed:
  after each managed resource is created, attach a scoped
  `azurerm_role_assignment` (`Storage Blob Data Contributor` for blob,
  Key Vault access policy/RBAC for secrets, etc.) to the consuming
  service's identity — mirroring `aws/permissions.go`'s
  `grantDatabasePermissions`/`grantS3Permissions` pattern
  (permissions.go:196-254). Note the identity-ordering constraint already
  solved for ACR auth (`docs/azure-todo.md:176-192`) doesn't apply here —
  a role assignment *can* reference the identity's `principal_id` after
  the Container App exists, in a second resource, the same way AWS
  attaches `IamRolePolicy` post-creation.
- [ ] **Route database/cache credentials through Key Vault instead of
  plaintext env vars.** Key Vault is provisioned every run
  (`inferKeyVault`, `azure/infer.go:114-124`) but
  `resources.KeyVaultSecret` is never populated
  (`grep -rn "KeyVaultSecret\[" internal/compiler/` — zero matches).
  `inferDatabasesAzure`/`inferCachesAzure` (managed.go:107-152, 222-270)
  inject the random password/access key directly as a plaintext
  Terraform-interpolated container env var
  (`containerSpecAzure`, compute.go:178-182). AWS's equivalent
  (`handleSecrets`, `aws/compute.go:291-337`) creates a Secrets Manager
  secret and wires it via `valueFrom`, never plaintext. Needs: a
  `KeyVaultSecret` per DB/cache credential, and container env vars that
  reference it (Container Apps' own `secretRef` mechanism), not the
  literal value.
- [ ] **Fix the `Relationship`→URL injection bug for non-Postgres
  targets.** `containerSpecAzure` (compute.go:111-186) hardcodes a
  `postgresql://user:pass@host:port/db` template for *every* relationship
  regardless of the target's actual capability — confirmed still present,
  deliberately ported bug-for-bug from Python (compute.go:103-110's own
  comment). A cache or object-storage relationship renders as
  `postgresql://None:None@<redis-host>:None/None`. This is currently
  shipping, not hypothetical — no example exercises
  `Relationship`→cache/storage today (hence no golden test catches it),
  but any real Azure app doing so will get a broken connection string.
  Fix: branch on the target service's `Capability` and render the correct
  URL scheme (or, better, adopt AWS's general `ResolveValue`/
  `shared.URLPattern`-based substitution instead of a hardcoded template
  — see the Priority 3 item on this below).

## Priority 2 — Missing features (compose directives with no Azure effect)

These are compose-file features that work on AWS and silently do nothing
on Azure — no error, just missing behavior.

- [ ] **Implement compose `secrets:` support for Azure.**
  `handleSecrets` (`aws/compute.go:291-337`) has no Azure equivalent —
  `grep -rn "service.Secrets" internal/compiler/azure/` returns zero
  matches. Needs: Key Vault secret per compose secret + `secretRef` in
  the container spec (naturally combines with the Key Vault work above).
- [ ] **Implement `x-composey` platform `config:` support for Azure.**
  `handlePlatformConfig` (`aws/compute.go:342-400`) likewise has no Azure
  equivalent. Same Key Vault-backed mechanism as secrets, once that
  exists. This is the direct reason `examples/platform-config` has no
  Azure golden file.
- [ ] **Wire `service.Size` into Azure database sizing.**
  `inferDatabasesAzure` hardcodes `B_Standard_B1ms` for every
  PostgreSQL/MySQL server regardless of declared size
  (`NewPostgreSQLFlexibleServer`, `models/azure.go:192-198`) —
  `service.Size` is never read in `inferDatabasesAzure` at all. AWS uses
  `shared.DBInstanceClasses[service.Size]` (`aws/managed.go:105-108`).
  This is the direct reason `examples/scaling` has no Azure golden file.
- [ ] **Add MariaDB detection to Azure's database engine inference.**
  `isMySQLImage` (`azure/managed.go:77-80`) only checks for `mysql` in
  the image name; a MariaDB image silently gets treated as PostgreSQL
  (AWS's `inferDatabase` correctly detects mariadb as a MySQL-family
  engine, `aws/managed.go:59-65`).
- [ ] **Implement Azure autoscaling for CPU/Memory metrics.**
  `inferContainerApps` (`azure/compute.go:364-374`) only handles
  `AutoScalingMetricRequestsPerTarget`; CPU/Memory entries in
  `service.AutoScaling.Metrics` are silently ignored (no error, no
  `custom_scale_rule` emitted) because **no model type exists** for a
  Container Apps CPU/memory custom scale rule at all — this needs a new
  struct in `models/azure.go` (something like
  `ContainerAppCustomScaleRule`) before the inference gap can even be
  closed. AWS handles all three metric types
  (`handleAutoscaling`, `aws/compute.go:531-601`).
- [ ] **Add a default autoscaling policy for Azure when none is declared.**
  AWS's `defaultAutoScalingConfig()` (`aws/compute.go:603-617`) applies
  CPU 70%/Memory 80% whenever `MaxScale>1` and no explicit policy is set.
  Azure has no equivalent — a service with `max_scale>1` but no
  `auto_scaling` block and no ingress gets zero scale rules (min/max
  replicas are honored, but nothing drives scaling from 1→N without an
  HTTP rule).

## Priority 3 — Architectural gaps (larger design work, not small fixes)

- [ ] **Design and implement an Azure WAF/security-policy equivalent.**
  Not a wiring gap — the resource type doesn't exist in the model at
  all. No `azurerm_cdn_frontdoor_firewall_policy`/`_security_policy`
  anywhere in `models/azure.go` or `azure/edge.go`. AWS creates a WAFv2
  Web ACL with AWS-managed rules alongside every CloudFront distribution
  (`aws/edge.go:28-59,96-97`). This is a bigger lift than most items here
  — needs new model structs, a new inference function, and a design
  decision on which managed rule set(s) to default to.
- [ ] **Design network-isolation enforcement for Azure (or explicitly
  document its absence as intentional).** `NetworkIsolationSegments`
  drives real per-segment security groups on AWS
  (`InferNetworking`, `aws/connectivity.go:45-100`); Azure has no
  equivalent file/mechanism at all — compose `networks:` isolation
  intent is silently dropped. Related: `models.Relationship`
  (`semantic.go:142-146`) claims to be "the single source of truth for
  network security" in its own docstring, but is enforced on none of the
  three clouds consistently (AWS via security groups; Azure not at all;
  GCP doesn't emit a `roles/run.invoker` binding either, per
  `docs/spikes/gcp/README.md`'s own 2026-08-07 status note). Worth
  deciding, across all three clouds at once, whether this docstring claim
  should be qualified (best-effort per target) or actually built out for
  Azure/GCP — see `docs/spikes/azure/README.md`'s finding #1 and
  `docs/spikes/gcp/README.md`'s reversal of it, both still open.
- [ ] **Generalize Azure's connection-string rendering** to use something
  closer to AWS's `ResolveValue`/`shared.URLPattern` general substitution
  (`aws/connections.go:57-125`) instead of the hardcoded Postgres-shaped
  template — this is the durable fix behind the Priority 1 URL-injection
  bug item above, not just a one-off patch for that bug.
- [ ] **Add private networking + RBAC for Azure Redis and Blob Storage.**
  No delegated subnet/private endpoint for Managed Redis at all (unlike
  databases, which do get `privateNetworkingAzure`,
  `azure/managed.go:22-69`, when the environment has the subnet IDs set).
  No `Storage Blob Data Contributor`-equivalent role assignment for blob
  access either — this was the spike's own explicitly recommended design
  (`docs/spikes/azure/README.md:line 153` area) and was never built.
  Naturally combines with the Priority 1 RBAC work.

## Priority 4 — Smaller robustness/consistency gaps

- [ ] **Consolidate Azure's size→CPU/memory table with AWS's
  `shared.SizeMappings`.** `getCPUCoresAzure`/`getMemoryGBAzure`
  (`azure/compute.go:214-244`) hardcode an independent table
  (small=0.25vCPU/0.5Gi, medium=0.5/1Gi, large=1.0/2Gi) that will silently
  drift from `shared.SizeMappings` if that's ever changed for AWS. Should
  derive from the shared table (converting units as needed), not
  duplicate it.
- [ ] **Add a size-ceiling rejection for Azure Container Apps.**
  Consumption-tier caps at 2 vCPU/4GiB; `size: large` maps to only
  1.0vCPU/2Gi today, so the current mapping never actually hits the
  ceiling — but an explicit `cpu`/`memory` override in `x-composey` could
  exceed it with no rejection path. Still-open finding from
  `docs/spikes/azure/README.md` (finding #3) and
  `docs/spikes/gcp/README.md`'s equivalent finding for GCP — worth fixing
  for both non-AWS clouds together, following the pattern
  `docs/authored-environment-config.md`'s "backends should be able to
  reject what they cannot express" recommendation already established.
- [ ] **Wire backup/HA settings for Azure databases.** `HighAvailability
  map[string]string` exists on the model (`azure.go:185`) but is never
  set by `inferDatabasesAzure` — dead field, same category as
  `RoleAssignment`. No backup-retention field exists on the model at all
  (AWS wires `SkipFinalSnapshot`/`FinalSnapshotIdentifier` from `discard`,
  `aws/managed.go:100-102,124-128`).
- [ ] **Wire `discard`/force-delete for Azure Storage Accounts.** AWS sets
  `ForceDestroy` from `discard` (`aws/managed.go:208`); Azure's
  `StorageAccount`/`StorageContainer` models have no equivalent field and
  `discard` is never passed into `inferStorageAzure` at all.
- [ ] **Consider a per-service log-retention override for Azure**, or
  explicitly document why the platform-owned Log Analytics retention
  (`azure/environment_generator.go:59-68`, hardcoded 30 days) is
  sufficient and AWS's per-service `env.LogRetentionDays`
  (`aws/compute.go:47-53`) is intentionally AWS-only. This may already be
  the intended design (per the GCP spike's own recommendation to move
  `LogRetentionDays` to `AwsEnvironment` — never implemented for that
  reason either) — worth a decision either way rather than leaving it
  ambiguous.
- [ ] **Add health-check/probe configuration for Azure Container Apps.**
  No liveness/readiness/startup probe is ever populated, and
  `StartupGracePeriod` is read nowhere in `azure/*.go`. Container Apps'
  schema supports probes; AWS's ALB target-group health check
  (`aws/compute.go:456-474`) and `HealthCheckGracePeriodSecs`
  (compute.go:419) have no Azure counterpart at all.

## Testing debt (a consequence of the gaps above, not independent)

- [ ] **Add `examples/compute-tuning/expected/azure/`** once Azure sizing
  is fixed (Priority 2/4) — currently untestable since the feature isn't
  implemented consistently.
- [ ] **Add `examples/platform-config/expected/azure/`** once `config:`
  support ships (Priority 2).
- [ ] **Add `examples/scaling/expected/azure/`** once `service.Size`
  flows into Azure DB sizing and CPU/Memory autoscaling exists
  (Priority 2).
- [ ] **Add dedicated Azure unit test files** mirroring AWS's structure —
  currently all Azure unit coverage is consolidated into one
  `coverage_test.go` (18 tests) vs AWS's 10 files/86 tests. Natural to do
  incrementally alongside each feature fix above rather than as a
  separate pass (write the test for `inferContainerApps`'s autoscaling
  when fixing autoscaling, etc.), rather than a big-bang test-writing
  exercise disconnected from real behavior changes.

## Explicitly not a gap (architectural differences confirmed intentional)

- Azure's shared-server-per-engine database topology (vs AWS's
  dedicated-instance-per-service) — documented as an intentional design
  choice in `plan.md`, not an oversight. Real capability tradeoff
  (services sharing an engine can't get independent HA/sizing/maintenance
  windows) but not something to "fix" without a product decision to
  change the topology itself.
- Azure's one-shared-ACR-per-environment vs AWS's one-ECR-repo-per-service
  — both valid, noted as a real difference in `docs/spikes/azure/README.md`,
  not a gap.
- No shared ALB/ingress equivalent created by `composey init --provider
  azure` — architecturally justified by Container Apps' per-service-FQDN
  model (no shared listener to configure), not a missing AWS parity
  feature.
- Rate-schedule rejection when a value doesn't divide evenly into cron
  (`azure/compute.go:35-62`) — a real expressiveness gap vs AWS's native
  `rate(...)`, but handled as a clear compile-time error, which is the
  correct behavior per this project's own "backends should reject what
  they cannot express" principle, not a bug to fix.
- Retries/timeouts unmodeled for scheduled jobs — absent on **both**
  clouds equally, not an Azure-specific gap.
