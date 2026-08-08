# Azure: where things stand

Written 2026-08-04, after taking Azure from "compiles but has never deployed"
to a five-example acceptance suite that passes against real Azure, including
Container Apps Jobs, image build-and-push, and Front Door (all confirmed via
full runs in `francecentral`).

> This doc tracks *deployment verification* — does Azure's output apply
> cleanly against real Azure. For *feature completeness* relative to
> AWS (missing RBAC, no WAF, silent no-ops for secrets/config, etc.),
> see `docs/azure-aws-parity-todo.md`.

## Verified against real Azure

Each of these deployed, served the expected response, and tore down cleanly
with nothing left in the subscription.

| Example | Proves |
| --- | --- |
| `hello` | Container App, ingress, the environment stack |
| `web-api` | Two services talking to each other |
| `production-stack` | PostgreSQL Flexible Server, Managed Redis, Key Vault, Container Apps Jobs, Front Door (`cdn: true`) |
| `minio-s3` | Container-to-managed-service substitution (minio → Storage Account) |
| `build-webapp` | Image build + push (`docker_image`/`docker_registry_image` → ACR), pull by digest |

Run one with:

```bash
gh workflow run azure-acceptance.yml --ref main -f example=hello
```

## Outstanding work

### 1. Front Door: confirm traffic actually flows through it, not just that it applies cleanly

`cdn: true` compiles to a real Front Door Standard setup — one
`azurerm_cdn_frontdoor_profile` per application, one endpoint + origin group
+ origin + route per CDN-enabled service, fronting that service's Container
App ingress FQDN (`_infer_cdn` in `compiler/inference/azure/__init__.py`).
Front Door is global, so none of these five resources carry a `location`,
unlike everything else this module creates. Replaces the old
`CdnProfile`/`CdnEndpoint` models, which targeted Azure CDN from Microsoft
(classic) — a product that no longer accepts new profiles at all.

Confirmed clean end-to-end against real Azure 2026-08-05 (`production-stack`
in `francecentral`), after finding and fixing two real, distinct bugs along
the way — full detail in git history (`7d1e301`, `3cc0721`, `387e17b`,
`6319a18`) and in "things worth knowing" below:

1. `azurerm_cdn_frontdoor_origin` is created with `enabled=false` regardless
   of configuration — an open, unresolved azurerm provider bug
   ([#31647](https://github.com/hashicorp/terraform-provider-azurerm/issues/31647)).
   Worked around in `scripts/smoke-test-azure.sh`: the app-stack apply
   detects this exact failure message and retries once.
2. That retry, applied blindly to the whole stack, re-touched
   `azurerm_postgresql_flexible_server` and hit a second, unrelated,
   long-standing provider bug over its auto-assigned `zone`. Fixed at the
   actual source — `PostgreSQLFlexibleServer` now carries
   `lifecycle.ignore_changes: ["zone"]` — rather than by scoping the retry,
   since `-target` turned out to pull in a resource's entire dependency
   chain regardless of how the retry was scoped.

**Still not fully verified even with a clean apply**: the smoke test polls
the Container App's own FQDN directly and has never actually sent a request
through the Front Door endpoint itself. A clean apply confirms the five
resources exist and correctly reference each other — it does not confirm
Front Door actually proxies real traffic to the Container App end to end.
Extend the smoke test to poll `azurerm_cdn_frontdoor_endpoint.<key>.host_name`
instead of (or as well as) the Container App's own FQDN before treating this
as fully proven.

### 2. Smaller things



- `nginx-flask-mysql` compiles and validates but has never had a live run. It
  is the only MySQL path, so the `mysql` delegated subnet and the MySQL private
  DNS zone are both untested. It is not in the acceptance menu because it names
  a local-only image.
- The persistent-environment restructure (see below) is now much less urgent.

## Things worth knowing before touching this again

**`generator_azure.py` was silently dropping every model's `lifecycle`
block.** `_clean_model` deleted the key outright with a "needs special
handling" comment, for every resource type, not just the one it was written
for. `KeyVaultSecret`'s `ignore_changes: ["value"]` had been silently
inert the whole time — no test caught it because nothing asserted its
presence in output, only that the model carried the field. Fixed by
deleting the special-case deletion; AWS's generator already emits
`lifecycle` as a plain key with no special handling, which is all Terraform
JSON needs. Found while chasing a Postgres `zone`-drift bug hit via Front
Door's retry path (below) — worth an audit of what else this cost, since it
went unnoticed until now.

**`-target` does not scope a retry the way it looks like it should.**
Tried using `-target=<one resource>` to retry only a resource that failed,
without touching anything else already applied. It still pulled in that
resource's entire dependency chain — anything the target references,
transitively, all the way back. If two resources are connected through
enough intermediate references (Front Door origin → Container App →
Postgres connection string, in this case), `-target` on one reaches the
other regardless of how unrelated they look. There is no way to retry "just
this resource" once anything else in the stack references what it depends
on; the actual fix has to prevent the retriggering plan from wanting to
change the other resource at all (`ignore_changes`, as landed on
`PostgreSQLFlexibleServer.zone`), not try to dodge it with more precise
targeting.

**Two open azurerm provider bugs, both confirmed against real Azure
2026-08-05, both worth knowing about before assuming a schema-valid Front
Door / Postgres config will apply cleanly:**
- `azurerm_cdn_frontdoor_origin` is created with `enabled=false` regardless
  of what's configured, so any route depending on it fails its first apply.
  A second apply always succeeds. Open:
  [hashicorp/terraform-provider-azurerm#31647](https://github.com/hashicorp/terraform-provider-azurerm/issues/31647).
  Worked around with a detect-and-retry in `scripts/smoke-test-azure.sh`.
- `azurerm_postgresql_flexible_server`'s `zone` is assigned by Azure itself;
  any plan that reaches the resource without `ignore_changes: ["zone"]`
  tries to write back whatever value Azure picked and gets rejected with
  "`zone` can only be changed when exchanged with the zone specified in
  `high_availability.0.standby_availability_zone`". Open on and off since
  2022, e.g. hashicorp/terraform-provider-azurerm#16888. Fixed for good on
  the model, not worked around per-call-site.

**Deploy to `francecentral` for anything with a cache, `uksouth` otherwise.**
`eastus` is offer-restricted for PostgreSQL Flexible Server —
`LocationIsOfferRestricted`, twenty minutes into an apply. Separately, Azure
Managed Redis (`azurerm_managed_redis`, both Balanced_B0 and B1) fails with
`InsufficientCapacity` in `uksouth` and `northeurope` — confirmed against real
Azure 2026-08-04, reproducible via plain `az redisenterprise create`, nothing
to do with Terraform or this codebase. The same SKU succeeded in `eastus`
within ~4 minutes, so it's regional physical capacity, not a SKU problem —
resist the urge to "fix" it by bumping the SKU, that just doubles cost for no
benefit. `francecentral` is the one region checked so far with neither
restriction: Postgres came up `Ready` and Redis came up `Running`, both within
minutes, same day. `uksouth` remains fine — and faster to build a Container
Apps Environment in (~3 min against 13–17 in `eastus`) — for examples that
don't touch Redis. `REGION` in the workflow controls this and is deliberately
separate from `STATE_LOCATION` (where the state blob lives).

**`terraform validate` cannot catch several whole classes of bug.** It passed,
happily, on: two stacks both declaring ownership of the Container Apps
Environment; a database pointed at a subnet delegated to another service; and
two Azure services that have been retired. A valid schema says nothing about
whether the service still exists or whether two stacks will collide. Every
one of those cost an hour to find. Assume a live run is required for anything
touching resource identity, networking, or a service you have not deployed
before.

**Azure retires things and the provider does not.** Two retired services turned
up in a single example (classic CDN, Azure Cache for Redis). The Azure
inference was written against documentation that has since moved. Storage, Key
Vault and Container Registry have not been near a live apply with anything
unusual configured — an audit would be cheaper than discovering the next one
an hour at a time.

**Provider is pinned to `azurerm ~> 4.0`** (`f547e65`). The bump was forced:
`azurerm_managed_redis` exists only in 4.x, and on 3.x the only route to
Managed Redis is `redis_enterprise_cluster`, which rejects the Balanced SKUs
and starts at ~$220/month against ~$13 for `Balanced_B0`. The whole migration
was two renamed properties (`https_traffic_only_enabled`,
`non_ssl_port_enabled`), both since applied against real Azure.

**The fast feedback loop.** Compile an example and run `terraform validate`
against it locally — seconds, no credentials, no Azure calls. Every schema-shaped
bug found in a 45-minute CI run this session was findable that way.
`tests/integration/test_validate_azure.py` does this for all ten examples; AWS
has had the equivalent since the start and Azure had none, which is most of why
the bugs accumulated.

**State and cleanup.** Terraform state lives in `composeyacceptstate/tfstate`
with Entra ID auth (shared-key access is disabled; identities need *Storage
Blob Data Contributor* on the account, not just Contributor on the resource
group). A clean teardown purges its own state; a failed one keeps it, because
that is the recovery path — `scripts/smoke-test-azure.sh --destroy-only`.

**Run `ruff format --check` before pushing.** CI enforces it and the test suite
does not. It has broken the build twice.

**A Container App/Job cannot bootstrap its own pull permission via its own
system-assigned identity.** `registry { server, identity: "System" }` needs
an `AcrPull` role assignment on that identity, but the identity's
`principal_id` does not exist until the resource is created, and creation
itself needs to pull the image — a real ordering cycle, not a missing role
assignment that could just be declared alongside it. It fails silently and
slowly: no permissions error, just `azurerm_container_app` (or `_job`)
stuck "Creating..." for the full 20-30 minute provider timeout before
`ContainerAppOperationError: Operation expired`. Confirmed against real
Azure 2026-08-04. Fixed for both resource types by authenticating with the
ACR's admin username/password instead (`_registry_auth` in
`inference/azure/__init__.py`) — stable resource attributes available the
moment the registry exists. A **user-assigned** identity would not have hit
this, since its `principal_id` exists independently of the resource being
granted the role — worth remembering if admin credentials ever need to go
away (ACR admin accounts are the kind of thing that eventually gets a
security finding).

## Deferred: persistent acceptance environment

Originally recommended because every run spent ~43 of its ~45 minutes creating
and destroying one resource. Moving to `uksouth` cut that to ~3 minutes for
free, so the payoff is much smaller now. The prerequisite — a real Terraform
state backend — was built (`67d62c4`) and is in use regardless.

Still worth doing if iteration becomes painful again: split environment
lifecycle from app lifecycle in the smoke script, give the environment a stable
name, add a workflow to create/refresh/destroy it. The trade is losing per-run
isolation, so a run that dies mid-teardown leaves the next one starting dirty.
