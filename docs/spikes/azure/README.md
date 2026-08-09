# Spike: can the semantic model express Azure?

**Status:** design spike, 2026-07-25. No compiler code, nothing deployed.
Azure was implemented shortly after this spike and has since been verified
against real Azure deployments (see `../../azure-todo.md`).

> **For current implementation status of any finding below, see
> `../../azure-aws-parity-todo.md`** — that doc is the actively-maintained,
> single source of truth for what's fixed and what's still open across
> both Azure and GCP. This spike is kept purely as the original
> design-time record (method + verdict); it is deliberately **not**
> re-annotated every time something below gets fixed, since doing that in
> two places (this file and the parity doc) drifted out of sync more than
> once in practice.

## Method

Three examples were chosen to stress different parts of the model, and the
Terraform that composey *should* produce for each on Azure was written by hand:

| Example | Stresses |
| --- | --- |
| `hello.tf` | the minimum path: one public container |
| `production-stack.tf` | cdn + waf, autoscaling, schedule, RDS + cache substitution, relationships |
| `doctor.tf` | build-from-source, object storage, health check grace period |

Each was then compared against the AWS output composey generates today. The
target platform is **Azure Container Apps** — the closest analogue to ECS
Fargate. ACI is too primitive (no ingress, no scaling) and AKS too heavy.

**Limitations.** The HCL is schema-approximate: it was not run through
`terraform validate` against the `azurerm` provider, and no Azure subscription
was involved. It is a modelling exercise, not a working backend.

## Verdict

**The capability vocabulary holds. The networking model does not.**

`container` / `database` / `cache` / `object-storage` mapped 1:1 with no new
capabilities needed, and `size`, `min_scale`, `max_scale`, `cdn`, `build`,
`secrets` and `storage` all survived contact. That is a genuinely good result:
the *shape* of `semantic.py` is right.

Five things did not survive. They are listed worst-first.

### 1. `Relationship` has no Azure enforcement point

`semantic.py` calls `Relationship` "the single source of truth for network
security and service discovery". That is an AWS statement. The AWS backend turns
each `depends_on` edge into a security group rule; `production-stack` produces
four.

Container Apps in one environment reach each other over internal DNS with **no
per-pair enforcement point**. The database and cache are reachable because they
sit on a delegated subnet, which is an environment-level decision made by the
platform team, not a service-pair one made by the compiler.

So `Relationship` degrades from a security primitive to a dependency hint. This
needs an explicit decision: either it is *intent* that each backend enforces as
precisely as it can (with Azure enforcing it barely), or the claim in the
docstring gets qualified. It cannot stay as written.

(See the [GCP spike](../gcp/README.md) for a related finding: GCP *does* have a
per-pair enforcement point available (`roles/run.invoker`), which is why this
question is "per target," not a single yes/no across all three clouds.)

### 2. `schedule` carries EventBridge syntax

Already suspected; now demonstrated. `production-stack` says
`cron(0 2 * * ? *)`, which is passed straight through to `schedule_expression`.
Azure needs `0 2 * * *` — the `cron(...)` wrapper and the AWS-only `?`
day-of-week placeholder are both meaningless there.

Worse, `rate(1 hour)` has no cron equivalent at all, so a string field cannot be
translated mechanically. The semantic model needs either standard 5-field cron
or a small structured type, with each backend rendering its own dialect.

### 3. `size: large` is not expressible

`large` is 4096 CPU units / 8192 MiB. A Container Apps replica on the
consumption profile caps at **2 vCPU / 4 GiB**; going beyond needs dedicated
workload profiles, which are an environment-level, platform-owned decision.

So a size in the shared vocabulary is not satisfiable on every target. Either a
backend must be able to *reject* a size with a clear error, or `Environment`
must declare which sizes it supports. Today neither is possible — `size` is a
`Literal` that inference silently maps to whatever it likes.

### 4. `health_check_grace_period` is an ECS concept

It means "seconds the *load balancer* health check is ignored after a task
starts". Container Apps has no grace period. The nearest expression is a startup
probe whose failure budget covers the same window — 120s becomes 12 failures at
a 10s interval, which is approximate rather than equivalent.

It should be restated cloud-neutrally (`startup_grace_seconds`) or dropped from
the semantic model and pushed into an AWS-specific escape hatch.

### 5. `public_service` is singular because AWS has a shared ALB

The model allows exactly one `public_service`, routed at `/*` on a shared
listener. That is an artifact of the shared-ALB design: one listener rule, one
priority, one path pattern.

Container Apps gives **every** app its own FQDN and terminates TLS itself. There
is no shared listener, no priority, and no reason to nominate one public
service. The model under-expresses here — Azure would happily expose several.

## What the spike also confirmed

- **Parallel backends, not shared inference.** Ingress is the clearest evidence:
  AWS emits 11 resources for `hello` (security group, egress rule, log group,
  two roles, exec policy, task definition, service, target group, listener rule,
  ingress rule); Azure emits **one**. There is nothing to share below the
  semantic model. This settles the question from the earlier discussion.
- **`BaseEnvironment` factored correctly.** `target`, `name`, `region` and
  `tags` were all reused unchanged. `AzureEnvironment` adds resource group,
  Container Apps environment, Key Vault, registry, and the delegated subnet plus
  private DNS zone for databases — all genuinely target-specific. The
  discriminator design holds. (`region` is called `location` in Azure and is
  usually inherited from the resource group; that is a rename, not a problem.)
- **Azure ships the named access tiers.** `Storage Blob Data Contributor`,
  `Key Vault Secrets User`, `AcrPull` are built-in RBAC roles, so there is no
  policy document to synthesise at all. This validates the "named access tiers
  instead of `s3:*`" idea borrowed from ecs_composex — and suggests the semantic
  model should express access *intent* (read / read-write) rather than anything
  policy-shaped.
- **Two AWS warts are Azure non-problems.** The CloudFront WAF must live in
  `us-east-1` behind an aliased provider; Front Door WAF policies sit in the
  application's own resource group. And an ECR repository per service has no ACR
  analogue, since one registry holds many repositories.
- **Log retention is platform-owned.** `retention_in_days = 7` is hardcoded in
  AWS inference, but on Azure the Log Analytics workspace belongs to the
  Container Apps environment. It belongs in `Environment`, not inference.
- **Endpoint injection gets more fragile, not less.** On AWS an S3 endpoint
  variable is optional because the SDK defaults work. Azure SDKs need the
  account endpoint explicitly, so the injected variable is load-bearing. The
  name-matching heuristic (`BUCKET`/`NAME`/`ENDPOINT`/`URL`) is doing more work
  across two clouds than one — strengthening the case for an explicit
  attribute-to-variable mapping.

## Recommended actions

**Worth doing now, independent of Azure** — each is small and each improves the
AWS backend on its own merits:

1. Normalise `schedule` to standard cron plus a structured rate, translating to
   EventBridge syntax in the AWS backend.
2. Restate or remove `health_check_grace_period`.
3. Move log retention from inference into `Environment`.

**Decide before any Azure backend is written:**

4. Is `Relationship` enforced or advisory? This is a product decision, not a
   technical one.
5. How does a backend reject a `size` it cannot satisfy?
6. Should `public_service` become a set?

**Explicitly not needed:** no new capabilities, and no change to the
compose-facing `x-cloud` surface. The parts of the design that users touch
came through unchanged.
