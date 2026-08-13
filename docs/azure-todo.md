# Azure: where things stand

Tracks *deployment verification* — does Azure's output apply cleanly
against real Azure. For *feature completeness* relative to AWS, see
`docs/azure-aws-parity-todo.md`.

## Verified against real Azure

Each of these has deployed, served the expected response, and torn down
cleanly with nothing left in the subscription:

| Example | Proves |
| --- | --- |
| `hello` | Container App, ingress, the environment stack |
| `web-api` | Two services talking to each other |
| `production-stack` | PostgreSQL Flexible Server, Managed Redis, Key Vault, Container Apps Jobs, Front Door (`cdn: true`), end-to-end traffic through Front Door's own CDN endpoint |
| `minio-s3` | Container-to-managed-service substitution (minio → Storage Account) |
| `build-webapp` | Image build + push (`docker_image`/`docker_registry_image` → ACR), pull by digest |

Run one with:

```bash
gh workflow run azure-acceptance.yml --ref main -f example=hello
```

## Open items

- **`nginx-flask-mysql`** compiles and validates but has never had a live
  run — the only example exercising the MySQL delegated subnet and MySQL
  private DNS zone. Not in the acceptance menu (names a local-only image).
- **Storage, Key Vault, and Container Registry** haven't been exercised
  with anything unusual configured. Worth an audit rather than
  discovering issues one at a time.
- **Persistent acceptance environment**: deferred. Moving CI to
  `francecentral`/`uksouth` cut per-run overhead enough that the
  create/destroy cost per run is no longer the bottleneck it was.

## Known upstream provider bugs (not fixable here)

- `azurerm_cdn_frontdoor_origin` is created with `enabled=false`
  regardless of configuration, so a route depending on it fails its
  first apply. A second apply always succeeds — worked around with a
  detect-and-retry in `scripts/smoke-test.sh`.
  [hashicorp/terraform-provider-azurerm#31647](https://github.com/hashicorp/terraform-provider-azurerm/issues/31647)
  (open).
- `azurerm_postgresql_flexible_server`'s `zone` is assigned by Azure
  itself; without `lifecycle.ignore_changes: ["zone"]` any later plan
  tries to write back Azure's own value and gets rejected. Fixed on the
  model (`PostgreSQLFlexibleServer`), not worked around per call site.
  Open upstream since 2022 (e.g. hashicorp/terraform-provider-azurerm#16888).

## Operational notes

- **Region**: deploy to `francecentral` for anything with a cache,
  `uksouth` otherwise. `eastus` is offer-restricted for PostgreSQL
  Flexible Server; Azure Managed Redis has seen `InsufficientCapacity`
  in `uksouth`/`northeurope`. `REGION` in the workflow controls this.
- **Fast feedback loop**: compile an example and run `terraform validate`
  locally before pushing — catches most schema-shaped bugs in seconds,
  no Azure credentials needed.
- **State and cleanup**: Terraform state lives in
  `cloudcomposeacceptstate/tfstate` with Entra ID auth (shared-key access
  disabled; identities need *Storage Blob Data Contributor*, not just
  Contributor on the resource group). A clean teardown purges its own
  state; a failed one keeps it — recover with
  `scripts/smoke-test.sh --destroy-only`.
- **A Container App/Job can't bootstrap its own ACR pull permission via
  its own system-assigned identity** — the identity's `principal_id`
  doesn't exist until the resource is created, but creation itself needs
  to pull the image. Fails silently: no error, just stuck "Creating..."
  for the full provider timeout. Fixed by authenticating with the ACR's
  admin username/password instead (`registryAuthAzure` in
  `azure/compute.go`) — stable attributes available the moment the
  registry exists. A user-assigned identity would avoid this.
- **CI service principal needs both a management-plane and data-plane
  Key Vault grant**: `Contributor` + `Role Based Access Control
  Administrator` (to create `azurerm_role_assignment` resources) is not
  enough on its own — Contributor's `dataActions` is empty, so Terraform
  itself can't read back `azurerm_key_vault_secret` resources it creates
  without an additional data-plane grant (`Key Vault Secrets Officer`).
  See `ci/README.md`'s Azure setup section for the full set of required
  grants.
- **Provider pinned to `azurerm ~> 4.0`**: `azurerm_managed_redis` only
  exists in 4.x; 3.x's `redis_enterprise_cluster` starts at ~$220/month
  against ~$13 for `Balanced_B0`.
