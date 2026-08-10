# Closing the Azure/AWS Feature Gap

> Analysis performed 2026-08-08, evidence-grounded in the current
> codebase (file:line citations throughout). Supersedes nothing —
> `docs/azure-todo.md` tracks *deployment verification* status (does the
> Azure backend's output actually apply against real Azure); this doc
> tracks *feature completeness* relative to AWS. Related open items
> already tracked elsewhere (`docs/spikes/azure/README.md`'s
> `Relationship` enforcement and size-ceiling findings, GCP's
> lighter-verification decision) are cross-referenced, not duplicated.

## Why this exists

AWS was implemented first and most rigorously; Azure got the same review
discipline for its initial implementation but has visibly fewer capabilities today.
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

> **Status (2026-08-08): all three items below are done.** See
> `internal/compiler/azure/permissions.go` (new file),
> `containerSpecAzure`'s rewrite in `compute.go`, and the model additions
> in `internal/models/azure.go` (`ContainerAppEnvVar.SecretName`,
> `ContainerAppSecret`'s Key-Vault fields, `KeyVault.RbacAuthorizationEnabled`).
> All 10 Azure golden fixtures regenerated and re-verified with
> `terraform validate` against the real `azurerm` provider schema.

These are the most serious: AWS enforces least-privilege IAM scoping and
routes secrets through Secrets Manager; Azure does neither for
managed-service access.

- [x] **Implement RBAC role assignments for managed-service access.**
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

  **Done via a user-assigned identity, not the ACR pattern's own
  workaround.** A genuine ordering cycle *does* exist here that the ACR
  case doesn't have: Container Apps resolves a Key Vault secret reference
  *as part of* the app's own creation, so the identity needs its role
  granted *before* that create — impossible for a system-assigned
  identity, whose `principal_id` doesn't exist until the resource it
  belongs to is created. Fixed by creating one `UserAssignedIdentity` per
  app (only when needed — see `inferManagedServiceIdentity`), granting it
  `Key Vault Secrets User`/`Storage Blob Data Contributor` first, then
  attaching it to only the specific services whose Relationships need it
  (`identityForService`) — every other service keeps its previous
  identity (system-assigned, or `env.UserAssignedIdentityID` if set).
- [x] **Route database/cache credentials through Key Vault instead of
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

  **Done.** `Key Vault` switched to RBAC authorization mode
  (`rbac_authorization_enabled = true`) rather than the classic
  access-policy model, so one RBAC primitive (`RoleAssignment`) covers
  both Key Vault and Storage. `storeManagedServiceSecret` creates a
  `KeyVaultSecret` per credential-bearing connection; `containerSpecAzure`
  now references it via `ContainerAppEnvVar.SecretName` +
  `ContainerAppSecret{KeyVaultSecretID, Identity}`, using the secret's
  `versionless_id` (not `id`) so a rotated password doesn't need a
  redeploy to take effect.
- [x] **Fix the `Relationship`→URL injection bug for non-Postgres
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

  **Done, via the narrower fix (branch on capability), not the broader
  Priority 3 rewrite** — `connectionURLAzure` now renders `redis://` for
  cache and a bare host for object storage; database connections that
  reach this function (i.e. have no stored Key Vault secret to use
  instead) still render `postgresql://`/`mysql://` correctly rather than
  always Postgres. In practice, every credential-bearing connection now
  goes through the Key Vault `secretRef` path above instead, so this
  function is mostly a fallback — but it's still correct if reached (e.g.
  a connection with no password at all). The broader Priority 3 item
  (adopting AWS's general `ResolveValue` substitution) remains open.

## Priority 2 — Missing features (compose directives with no Azure effect)

> **Status (2026-08-08): all six items below are done.** See
> `internal/compiler/azure/permissions.go` (extended),
> `internal/compiler/azure/managed.go`/`compute.go`, and
> `internal/models/azure.go` (`ContainerAppCustomScaleRule`,
> `MySQLFlexibleServerStorage`, `PublicNetworkAccess`). Two new example
> fixtures added (`examples/scaling`, `examples/platform-config`), plus
> **three additional real bugs found and fixed along the way** by
> validating every regenerated fixture with `terraform validate` against
> the real `azurerm` provider, not just the Go test suite — see the
> per-item notes and "Bugs found beyond the original scope" below.

These are compose-file features that work on AWS and silently do nothing
on Azure — no error, just missing behavior.

- [x] **Implement compose `secrets:` support for Azure.**
  `handleSecrets` (`aws/compute.go:291-337`) has no Azure equivalent —
  `grep -rn "service.Secrets" internal/compiler/azure/` returns zero
  matches. Needs: Key Vault secret per compose secret + `secretRef` in
  the container spec (naturally combines with the Key Vault work above).

  **Done** via `grantServiceSecretPermissions` (`permissions.go`): one
  Key Vault secret per compose `secrets:` entry, placeholder value
  (`PLACEHOLDER_VALUE_CHANGE_IN_AZURE_PORTAL` — a separate,
  Azure-correct constant from AWS's `shared.SecretsPlaceholderValue`,
  whose own wording says "CHANGE IN AWS CONSOLE"), wired via
  `ContainerAppEnvVar.SecretName`.
- [x] **Implement `x-cloud` platform `config:` support for Azure.**
  `handlePlatformConfig` (`aws/compute.go:342-400`) likewise has no Azure
  equivalent. Same Key Vault-backed mechanism as secrets, once that
  exists. This is the direct reason `examples/platform-config` has no
  Azure golden file.

  **Done** via `grantPlatformConfigPermissions` (`permissions.go`) —
  one Key Vault secret per config key, unlike AWS's single
  JSON-blob-sliced-by-key Secrets Manager secret: `azurerm_key_vault_secret`
  has no equivalent of Secrets Manager's `arn:...:key::` addressing into
  a JSON value, so one secret per key is the natural shape here instead
  of one secret containing all of them.
- [x] **Wire `service.Size` into Azure database sizing.**
  `inferDatabasesAzure` hardcodes `B_Standard_B1ms` for every
  PostgreSQL/MySQL server regardless of declared size
  (`NewPostgreSQLFlexibleServer`, `models/azure.go:192-198`) —
  `service.Size` is never read in `inferDatabasesAzure` at all. AWS uses
  `shared.DBInstanceClasses[service.Size]` (`aws/managed.go:105-108`).
  This is the direct reason `examples/scaling` has no Azure golden file.

  **Done** via `azureDBSkuFor`/`largestServiceSize` (`managed.go`).
  Azure's shared-server-per-engine topology (an intentional AWS/Azure
  difference — see "explicitly not a gap" below) raised a real design
  question with no AWS equivalent: what size should a server shared by
  several services be? Resolved as "sized for the largest consumer" —
  under-provisioning a shared resource is a worse failure mode than
  over-provisioning it for the smallest one.
- [x] **Add MariaDB detection to Azure's database engine inference.**
  `isMySQLImage` (`azure/managed.go:77-80`) only checks for `mysql` in
  the image name; a MariaDB image silently gets treated as PostgreSQL
  (AWS's `inferDatabase` correctly detects mariadb as a MySQL-family
  engine, `aws/managed.go:59-65`).

  **Done.** `isMySQLImage` now also matches `mariadb`. Confirmed as a
  real (not hypothetical) fix: the `flask` and `nginx-flask-mysql`
  examples both actually use a `mariadb:*` image and were silently
  provisioning a PostgreSQL server before this fix — their regenerated
  golden fixtures changed engine as a direct result, not just added new
  fields.
- [x] **Implement Azure autoscaling for CPU/Memory metrics.**
  `inferContainerApps` (`azure/compute.go:364-374`) only handles
  `AutoScalingMetricRequestsPerTarget`; CPU/Memory entries in
  `service.AutoScaling.Metrics` are silently ignored (no error, no
  `custom_scale_rule` emitted) because **no model type exists** for a
  Container Apps CPU/memory custom scale rule at all — this needs a new
  struct in `models/azure.go` (something like
  `ContainerAppCustomScaleRule`) before the inference gap can even be
  closed. AWS handles all three metric types
  (`handleAutoscaling`, `aws/compute.go:531-601`).

  **Done.** Added `models.ContainerAppCustomScaleRule` (KEDA's generic
  `cpu`/`memory` scaler shape: `metadata: {type: "Utilization", value:
  "<pct>"}`, confirmed against KEDA's own docs, not guessed) and wired
  both metric types in `inferContainerApps`.
- [x] **Add a default autoscaling policy for Azure when none is declared.**
  AWS's `defaultAutoScalingConfig()` (`aws/compute.go:603-617`) applies
  CPU 70%/Memory 80% whenever `MaxScale>1` and no explicit policy is set.
  Azure has no equivalent — a service with `max_scale>1` but no
  `auto_scaling` block and no ingress gets zero scale rules (min/max
  replicas are honored, but nothing drives scaling from 1→N without an
  HTTP rule).

  **Done** via `defaultAutoScalingConfigAzure()`, mirroring AWS's own
  default exactly (CPU 70%, Memory 80%) minus the cooldown fields (no
  Container Apps/KEDA equivalent per-rule — see Priority 4's granularity
  note).

### Bugs found beyond the original scope

Found while validating every regenerated fixture with `terraform
validate` against the real `azurerm` provider (not just this project's
own Go test suite) — none were anticipated by the original gap analysis,
and none are hypothetical:

- **Two duplicate `azurerm_role_assignment` resources for any app with
  both a database and cache relationship.** `grantManagedServicePermissions`
  (added in the Priority 1 PR) granted a separate `RoleAssignment` per
  connection, but every credential-bearing connection shares the same
  Key Vault, principal, and role — Azure's ARM API rejects two
  `RoleAssignment`s with an identical `(principal_id, role_definition_name,
  scope)` triple as a duplicate. Confirmed present in the already-merged
  `doctor`/`production-stack` golden fixtures. Fixed by granting Key
  Vault access at most once per app (`grantKeyVaultAccessOnce`).
- **`azurerm_mysql_flexible_server`'s `storage_mb` attribute doesn't
  exist.** Unlike `azurerm_postgresql_flexible_server` (flat
  `storage_mb`/`storage_tier`), MySQL Flexible Server's storage is a
  nested `storage { size_gb }` block. `terraform validate` rejected the
  old flat field outright ("Extraneous JSON object property") the moment
  the MariaDB fix above started routing real example apps through this
  code path for the first time — previously untested because nothing
  exercised it.
- **`azurerm_mysql_flexible_database`'s `server_id` attribute doesn't
  exist either.** Unlike PostgreSQL's equivalent (a single `server_id`
  reference), the MySQL database resource identifies its parent server
  via `resource_group_name` + `server_name` — two separate attributes.
- **`azurerm_mysql_flexible_server.public_network_access_enabled` is
  computed-only.** Unlike PostgreSQL's settable bool of the same name,
  MySQL's equivalent field is a `public_network_access` string
  (`"Enabled"`/`"Disabled"`) — the bool field can't be set at all
  (`terraform validate`: "Value for unconfigurable attribute"). Also
  discovered along the way: the provider docs state this is
  automatically set to `Disabled` whenever VNet-integrated, so it's now
  only set explicitly for the public-access case.
- **`NewMySQLFlexibleServer()`'s default `version: "8.0"` isn't a valid
  version string.** The provider only accepts `"5.7"`, `"8.0.21"`, or
  `"8.4"` — confirmed against the real provider schema/docs, not
  guessed. Fixed to `"8.0.21"`.

All four MySQL Flexible Server bugs above are notable for being
*pre-existing*, not introduced by this session's changes — they went
unnoticed because, before the MariaDB detection fix, nothing in the
example suite actually exercised the MySQL Flexible Server code path at
all (every `mysql`/`mariadb`-imaged example had been silently
misclassified as PostgreSQL). Fixing one gap surfaced four more that had
been latent and untested since MySQL support was first ported.

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
  GCP doesn't emit a `roles/run.invoker` binding either — confirmed
  directly in `gcp/infer.go`, not via a stale doc annotation). Worth
  deciding, across all three clouds at once, whether this docstring claim
  should be qualified (best-effort per target) or actually built out for
  Azure/GCP — see `docs/spikes/azure/README.md`'s finding #1 and
  `docs/spikes/gcp/README.md`'s reversal of it for the original design
  reasoning, both still open in code.
- [ ] **Generalize Azure's connection-string rendering** to use something
  closer to AWS's `ResolveValue`/`shared.URLPattern` general substitution
  (`aws/connections.go:57-125`) instead of the hardcoded Postgres-shaped
  template — this is the durable fix behind the Priority 1 URL-injection
  bug item above, not just a one-off patch for that bug.
- [x] **Add private networking + RBAC for Azure Redis and Blob Storage.**
  No delegated subnet/private endpoint for Managed Redis at all (unlike
  databases, which do get `privateNetworkingAzure`,
  `azure/managed.go:22-69`, when the environment has the subnet IDs set).
  No `Storage Blob Data Contributor`-equivalent role assignment for blob
  access either — this was the spike's own explicitly recommended design
  ("Azure ships the named access tiers" in `docs/spikes/azure/README.md`)
  and was never built.
  Naturally combines with the Priority 1 RBAC work.

  **RBAC half was already done in the Priority 1 PR** (`grantManagedServicePermissions`
  grants `Storage Blob Data Contributor` per storage relationship) —
  this item's own description was stale by the time it was picked up.
  **Private networking for Redis done 2026-08-08**: unlike Flexible
  Server (which takes `delegated_subnet_id`/`private_dns_zone_id`
  directly on the server resource), `azurerm_managed_redis` has no
  networking attributes at all beyond `public_network_access` —
  confirmed against the real provider schema, not assumed from the
  naming symmetry with the database case. Private connectivity is a
  genuinely separate `azurerm_private_endpoint` resource
  (`models.PrivateEndpoint`, new), attached to a plain (non-delegated)
  subnet, targeting `Microsoft.Cache/RedisEnterprise`'s `redisEnterprise`
  subresource with private DNS zone `privatelink.redis.azure.net` — both
  values confirmed against Microsoft's own private-endpoint DNS
  reference, not guessed. `env.RedisSubnetID` (new field) gates this the
  same way `PostgresqlSubnetID`/`MysqlSubnetID` already gate database
  private networking; `cloudcompose init` (`provider: azure` in
  `environment.yaml`) now creates a
  4th (non-delegated) subnet for it automatically, matching the existing
  Postgres/MySQL/Container-Apps subnet pattern. Verified end-to-end with
  `terraform validate` against both the environment-bootstrap output and
  a manually-generated app output with the subnet set.

## Priority 4 — Smaller robustness/consistency gaps

- [x] **Consolidate Azure's size→CPU/memory table with AWS's
  `shared.SizeMappings`.** `getCPUCoresAzure`/`getMemoryGBAzure`
  (`azure/compute.go:214-244`) hardcode an independent table
  (small=0.25vCPU/0.5Gi, medium=0.5/1Gi, large=1.0/2Gi) that will silently
  drift from `shared.SizeMappings` if that's ever changed for AWS. Should
  derive from the shared table (converting units as needed), not
  duplicate it.

  **Done, and the drift had already happened**: AWS's `medium` is
  1024 CPU units = 1.0 vCPU; Azure's own independent table said 0.5 —
  half, not matching, despite the same size name. Both functions now
  derive from `shared.SizeMappings` (converting ECS CPU units → vCPU
  cores, MiB → GiB) instead of a separate table.
- [x] **Add a size-ceiling rejection for Azure Container Apps.**
  Consumption-tier caps at 2 vCPU/4GiB; `size: large` maps to only
  1.0vCPU/2Gi today, so the current mapping never actually hits the
  ceiling — but an explicit `cpu`/`memory` override in `x-cloud` could
  exceed it with no rejection path. Still-open finding from
  `docs/spikes/azure/README.md` (finding #3) and
  `docs/spikes/gcp/README.md`'s equivalent finding for GCP — worth fixing
  for both non-AWS clouds together, following the pattern
  `docs/authored-environment-config.md`'s "backends should be able to
  reject what they cannot express" recommendation already established.

  **Done for Azure** (GCP's equivalent not attempted here) — both
  functions now return `(value, error)` and reject with a clear message
  when a size *or* an explicit `cpu:`/`memory:` override would exceed
  2 vCPU / 4GiB. Threading the error return up through
  `containerSpecAzure` → `inferContainerApps`/`inferScheduledJobs` →
  `InferAzure` was itself the main shape of this change (none of these
  returned an error before). Directly caught the `scaling` example's
  `web` service (`size: large` = 4 vCPU) as a real, correct rejection —
  removed from `azureGoldenExamples` since there's no valid Azure output
  to golden-test against (see `TestGetCPUCoresAzure_RejectsSizeAboveConsumptionCap`
  for the dedicated test instead).

  **New gap found while doing this, not fixed here**: Azure Container
  Apps' Consumption tier requires CPU and memory to be an *exact matched
  pair* from a fixed table (0.25vCPU/0.5Gi, 0.5/1.0Gi, ..., 2.0/4.0Gi) —
  not just independently under the 2vCPU/4GiB cap. This is **not
  enforced by Terraform's schema** (`terraform validate` passes
  regardless) — only by Azure's own API at `apply` time. Confirmed via
  the `compute-tuning` example's `worker` service (`size: medium` = 1.0
  vCPU + an explicit `memory: 4096` override = 4Gi): `terraform validate`
  accepts `cpu=1, memory="4096Mi"` even though 1.0vCPU only pairs validly
  with 2.0Gi. This would fail at real `apply` time and Cloud Compose
  Compiler has no way to catch it today. Deliberately not fixed in this
  pass (scope decision, not an oversight) — tracked here as a new,
  still-open item.

  **Done (2026-08-10).** `azureCPUMemoryPairAzure` (`azure/compute.go`)
  validates the resolved (cpu, memory) pair together — checking CPU
  lands on a 0.25 vCPU step and that memory equals exactly `2 × cpu` GiB,
  both confirmed against Microsoft's own vCPU/memory allocation table
  (learn.microsoft.com/azure/container-apps/containers), not guessed.
  `resolveContainerResourcesAzure` is the one call site
  (`containerSpecAzure`, shared by both Container Apps and Jobs) that
  resolves and validates the pair together, replacing two independent
  calls to `getCPUCoresAzure`/`getMemoryGBAzure` that each only checked
  their own value against the ceiling. `compute-tuning`'s `worker`
  service now correctly rejects (the exact bug this item found) —
  removed from `azureGoldenExamples` the same way `scaling` was for its
  own Priority 4 rejection, since there's no valid Azure output to
  golden-test against; see
  `TestResolveContainerResourcesAzure_RejectsMismatchedCpuMemoryPair`
  and the rest of `compute_resources_test.go` for dedicated coverage
  instead.

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

> **Status (2026-08-08): all done, though `scaling` was later removed
> again.** `examples/{platform-config,compute-tuning}/expected/azure/main.tf.json`
> added, registered in `azureGoldenExamples`, and independently
> `terraform validate`d. `examples/scaling` initially got an Azure
> fixture too, but was removed the same day once the Priority 4
> size-ceiling rejection landed: its `web` service's `size: large`
> (4 vCPU) is now a correct, intentional rejection on Azure, not a value
> to golden-test — see the size-ceiling item above and
> `TestGetCPUCoresAzure_RejectsSizeAboveConsumptionCap`. Note on
> `compute-tuning` specifically: checked before assuming it was blocked
> by the sizing gap, and it wasn't — container-level `cpu`/`memory`
> overrides already worked correctly on Azure; added anyway once
> confirmed nothing else was stopping it.

- [x] **Add `examples/compute-tuning/expected/azure/`** once Azure sizing
  is fixed (Priority 2/4) — currently untestable since the feature isn't
  implemented consistently.
- [x] **Add `examples/platform-config/expected/azure/`** once `config:`
  support ships (Priority 2).
- [x] **Add `examples/scaling/expected/azure/`** once `service.Size`
  flows into Azure DB sizing and CPU/Memory autoscaling exists
  (Priority 2).
- [ ] **Add dedicated Azure unit test files** mirroring AWS's structure —
  currently all Azure unit coverage is consolidated into one
  `coverage_test.go` (18 tests) vs AWS's 10 files/86 tests. Natural to do
  incrementally alongside each feature fix above rather than as a
  separate pass (write the test for `inferContainerApps`'s autoscaling
  when fixing autoscaling, etc.), rather than a big-bang test-writing
  exercise disconnected from real behavior changes.

  Partially addressed as a side effect of Priority 2 (a new
  `priority2_test.go`, 14 tests, was added alongside the feature work
  rather than folded into `coverage_test.go` — the "do it incrementally"
  approach this item recommended), and again by the Priority 4
  CPU/memory-pair fix (a new `compute_resources_test.go`, 8 tests, added
  the same way), but Azure unit coverage is still not split into
  per-concern files the way AWS's is. Left open.

## Explicitly not a gap (architectural differences confirmed intentional)

- Azure's shared-server-per-engine database topology (vs AWS's
  dedicated-instance-per-service) — an intentional design choice, not an
  oversight (see `azure/managed.go`'s own doc comment on
  `largestServiceSize`). Real capability tradeoff
  (services sharing an engine can't get independent HA/sizing/maintenance
  windows) but not something to "fix" without a product decision to
  change the topology itself.
- Azure's one-shared-ACR-per-environment vs AWS's one-ECR-repo-per-service
  — both valid, noted as a real difference in `docs/spikes/azure/README.md`,
  not a gap.
- No shared ALB/ingress equivalent created by `cloudcompose init` for Azure
  — architecturally justified by Container Apps' per-service-FQDN
  model (no shared listener to configure), not a missing AWS parity
  feature.
- Rate-schedule rejection when a value doesn't divide evenly into cron
  (`azure/compute.go:35-62`) — a real expressiveness gap vs AWS's native
  `rate(...)`, but handled as a clear compile-time error, which is the
  correct behavior per this project's own "backends should reject what
  they cannot express" principle, not a bug to fix.
- Retries/timeouts unmodeled for scheduled jobs — absent on **both**
  clouds equally, not an Azure-specific gap.
