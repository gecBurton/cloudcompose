# Spike: can the semantic model express Azure?

**Status:** design spike, 2026-07-25. No compiler code, nothing deployed.
Azure was implemented shortly after this spike and has since been verified
against real Azure deployments (see `docs/azure-todo.md`); annotations below note
which of this spike's findings were addressed and which are still open in
the shipped Go code (checked 2026-08-07).

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

> **Superseded in part by the [GCP spike](../gcp/README.md).** GCP *does* have a
> per-pair enforcement point (`roles/run.invoker` per calling service account),
> so two of three clouds enforce this and Azure is the outlier. `Relationship`
> should keep its place in the model; only the absoluteness of the docstring
> needs qualifying. The finding below still stands for Azure specifically.

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

> **Status (2026-08-07): still open.** `models.Relationship` (`semantic.go`)
> is unchanged — still no enforcement-mode field or qualified docstring —
> and GCP's inference (`gcp/infer.go`) does not emit a `roles/run.invoker`
> binding, so the "two of three clouds enforce it" resolution proposed by
> the GCP spike was never implemented in code.

### 2. `schedule` carries EventBridge syntax

Already suspected; now demonstrated. `production-stack` says
`cron(0 2 * * ? *)`, which is passed straight through to `schedule_expression`.
Azure needs `0 2 * * *` — the `cron(...)` wrapper and the AWS-only `?`
day-of-week placeholder are both meaningless there.

Worse, `rate(1 hour)` has no cron equivalent at all, so a string field cannot be
translated mechanically. The semantic model needs either standard 5-field cron
or a small structured type, with each backend rendering its own dialect.

> **Status: resolved.** `models.Schedule` (`semantic.go`) is now a
> cloud-neutral interface with `CronSchedule{Expression}` (standard 5-field
> cron) and `RateSchedule{Value, Unit}`. AWS-only EventBridge dialect
> translation (the `cron(...)` wrapper, `?` placeholder, 6-field cron) is
> isolated to `aws/scheduling.go`.

### 3. `size: large` is not expressible

`large` is 4096 CPU units / 8192 MiB. A Container Apps replica on the
consumption profile caps at **2 vCPU / 4 GiB**; going beyond needs dedicated
workload profiles, which are an environment-level, platform-owned decision.

So a size in the shared vocabulary is not satisfiable on every target. Either a
backend must be able to *reject* a size with a clear error, or `Environment`
must declare which sizes it supports. Today neither is possible — `size` is a
`Literal` that inference silently maps to whatever it likes.

> **Status: still open.** `getCPUCoresAzure`/`getMemoryGBAzure`
> (`azure/compute.go`) and GCP's `cpuLimitGcp`/`memoryLimitGcp`
> (`gcp/infer.go`) still silently map `size: large` to a fixed value with
> no ceiling check or rejection path on any backend.

### 4. `health_check_grace_period` is an ECS concept

It means "seconds the *load balancer* health check is ignored after a task
starts". Container Apps has no grace period. The nearest expression is a startup
probe whose failure budget covers the same window — 120s becomes 12 failures at
a 10s interval, which is approximate rather than equivalent.

It should be restated cloud-neutrally (`startup_grace_seconds`) or dropped from
the semantic model and pushed into an AWS-specific escape hatch.

> **Status: resolved.** Renamed to `StartupGracePeriod` in `models.Service`
> (`semantic.go`); the AWS backend maps it to the ECS-specific
> `HealthCheckGracePeriodSecs` (`aws/compute.go`).

### 5. `public_service` is singular because AWS has a shared ALB

The model allows exactly one `public_service`, routed at `/*` on a shared
listener. That is an artifact of the shared-ALB design: one listener rule, one
priority, one path pattern.

Container Apps gives **every** app its own FQDN and terminates TLS itself. There
is no shared listener, no priority, and no reason to nominate one public
service. The model under-expresses here — Azure would happily expose several.

> **Status: partially resolved.** `Application.PublicServices()`
> (`semantic.go`) already returns a slice, not a single value, so the
> model itself supports more than one public service. Whether AWS's
> shared-ALB backend and Azure/GCP's per-service-FQDN backends all handle
> N>1 correctly is untested.

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

> **Status (2026-08-07):** items 1-2 shipped; 3 did not (see the GCP
> spike's revision on this point); 4-6 are still open, per the
> per-finding annotations above.

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
compose-facing `x-composey` surface. The parts of the design that users touch
came through unchanged.
