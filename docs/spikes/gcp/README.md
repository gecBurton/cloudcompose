# Spike: can the semantic model express GCP?

**Status:** design spike, 2026-07-25. No compiler code, nothing deployed.
Companion to [`../azure/README.md`](../azure/README.md), same method: the same
three examples, hand-written by hand as the Terraform composey *should* produce,
then compared against the AWS output it produces today. GCP was implemented
shortly after this spike (with lighter verification than AWS/Azure — see
`plan.md`'s Phase 4 GCP section); annotations below note which of this
spike's findings were addressed in the shipped Go code (checked 2026-08-07).

Target platform is **Cloud Run**. GKE Autopilot is too heavy and Cloud Run is
the direct analogue of Fargate and Container Apps.

**Limitations.** Schema-approximate, not run through `terraform validate`
against the `google` provider, no GCP project involved.

## Verdict

**The capability vocabulary holds for a third cloud. Two new modelling faults
appeared, and one conclusion from the Azure spike is now reversed.**

### Reversal: `Relationship` should stay a security primitive

The Azure spike found no per-pair enforcement point and concluded `Relationship`
degrades to a dependency hint. **GCP has one.** A Cloud Run service is private
by default, and every caller needs an explicit `roles/run.invoker` binding for
its own service account. A `Relationship` client → server compiles to exactly
one IAM member binding — a direct analogue of the AWS security group rule, and
arguably a better one, since it is identity-based rather than network-based.

So two of three clouds enforce it and Azure is the outlier. `Relationship`
should keep its place in the model, with the docstring qualified to say
enforcement is best-effort per target rather than dropping the claim.

> **Status: still open.** This reversal was never implemented in code:
> `models.Relationship` (`semantic.go`) carries no enforcement-mode field
> or qualified docstring, and GCP's inference (`gcp/infer.go`) does not
> emit a `roles/run.invoker` binding — so the "GCP enforces it" half of
> this finding isn't actually true of the shipped compiler yet.

### New fault 1: `RateSchedule` is not always renderable

This one is a direct consequence of yesterday's fix, so it deserves plain
statement: removing the AWS dialect was right, but the neutral model has a
corner that at least one backend cannot render.

Cloud Scheduler accepts **standard cron only** — there is no rate concept. Most
intervals convert fine (`every 30 minutes` → `*/30 * * * *`, `every 6 hours` →
`0 */6 * * *`), but an interval that does not divide its unit evenly has no cron
equivalent at all. `every 90 minutes` is not expressible.

Options: constrain `RateSchedule` to values that divide evenly, convert to cron
in the normalizer and drop the rate form entirely, or let a backend reject an
unrenderable schedule with a clear error. The third is most honest and matches
what a `size` ceiling needs anyway (see below).

> **Status: moot for now.** GCP has no schedule/Cloud Scheduler inference
> at all yet — `gcp/infer.go` contains no schedule-rendering code, so
> scheduled services simply aren't handled on GCP, and this question
> doesn't arise in practice. Azure and AWS *do* both reject unrenderable
> schedules with clear errors (`azure/compute.go`'s `cronExpressionAzure`,
> `aws/scheduling.go`'s `cronExpression`), so the rejection pattern this
> section recommends is implemented — just not for GCP.

### New fault 2: a database endpoint is not always a hostname

`inference.py` injects `aws_db_instance.address` — a host — into whatever
environment variable looks like it wants one. That assumes every target
addresses a database over TCP by hostname.

The **idiomatic** Cloud Run route to Cloud SQL is the built-in connector, which
mounts a unix socket: the app connects to `/cloudsql/<connection_name>`, not to
a host and port. Private IP is available and does give a hostname, so this is
expressible — but the natural path is not.

This is the strongest evidence yet that the endpoint-injection design needs
attention. A managed service's connection details are a small structured thing
(host, port, socket path, database name, credential reference), not a string to
be substituted by name-matching heuristics. It is the same conclusion the
ecs_composex comparison reached from a different direction.

> **Status: still open.** `models.Connection` (`semantic.go`) is a flat
> struct closer to a descriptor than a raw string, but injection is still
> name-matching: AWS's `ResolveValue`/`URLPattern` (`aws/connections.go`)
> and `Connection.BareReference()` substitute by matching env-var names
> like `BUCKET`/`URL`. GCP's `buildConnectionURLGcp` (`gcp/infer.go`)
> hardcodes a `postgresql://` URL shape regardless of capability, with no
> unix-socket/Cloud SQL connector path implemented.

### Confirmed from the Azure spike

- **`size` needs a per-backend rejection mechanism.** `large` (4096/8192) is
  fine on Cloud Run, which allows up to 8 vCPU, but breaches the 2 vCPU ceiling
  on a Container Apps consumption replica. So the constraint is per-target, not
  universal — a backend must be able to refuse a size with a clear error.
  (Still open as of 2026-08-07: no backend implements size-ceiling rejection.)
- **`public_service` is singular only because AWS has a shared ALB.** Cloud Run
  gives every service its own HTTPS URL.
- **`startup_grace_period` is the right rename.** Cloud Run has a genuine
  startup probe, so the neutral name reads better here than the ECS one did.
- **No exec-role/task-role split**, again: one service account, permissions
  bound at the target resource.
- **Predefined roles ship with the platform** (`roles/storage.objectAdmin`,
  `roles/secretmanager.secretAccessor`), so there is no policy document to
  synthesise. Third cloud, third confirmation that access should be expressed as
  intent rather than as policy.

### New, smaller findings

- **`cdn: true` is not self-sufficient on GCP.** A Google-managed certificate
  requires a domain you own; there is no equivalent of the free
  `*.cloudfront.net` or `*.azurefd.net` hostname. Enabling the CDN therefore
  needs a `domain` the model has no field for.
- **CDN footprint varies wildly**: 2 resources on AWS, 5 on Azure, 7 on GCP
  (serverless NEG, Cloud Armor policy, backend service, URL map, certificate,
  target proxy, forwarding rule). Not a model fault, but a reminder of how much
  `cdn: true` is hiding.
- **`log_retention_days` is ignored by a second target.** Cloud Logging
  retention is a project-level policy, as Log Analytics retention is an
  environment-level one on Azure. Having just moved this field to
  `BaseEnvironment`, the evidence now says it belongs on `AwsEnvironment`: AWS
  is the only target where the compiled application owns its log store.
- **One port per service.** Cloud Run routes to a single container port and
  injects `PORT`. composey already takes `ports[0]`, so nothing breaks, but
  multi-port services are unrepresentable on this target.

> **Status (2026-08-07) on the two items above with a code-checkable
> claim:** both still open. `models.Service.CDNEnabled` (`semantic.go`) is
> still a bare bool with no `domain` field anywhere in the models package,
> and GCP's CDN/load-balancer path (`gcp/infer.go`) remains an explicit
> no-op. `LogRetentionDays` still exists on all three environment types
> (`models/environment.go`), not narrowed to `AwsEnvironment` — Azure's
> platform generator hardcodes its own 30-day retention independent of it,
> and GCP's inference never reads it at all.

## Three-cloud comparison

Resources emitted for `examples/hello`:

| | AWS | Azure | GCP |
| --- | --- | --- | --- |
| resources | 11 | 1 | 2 |

| Concern | AWS | Azure | GCP |
| --- | --- | --- | --- |
| compute | ECS Fargate | Container Apps | Cloud Run |
| ingress | ALB + TG + listener rule + SG | built-in | built-in + IAM invoker |
| identity | task role + exec role | managed identity | service account |
| access control | synthesised IAM policy | built-in RBAC role | predefined role |
| database | RDS | Flexible Server | Cloud SQL |
| cache | ElastiCache | Cache for Redis | Memorystore |
| object storage | S3 | Blob container | GCS bucket |
| secrets | Secrets Manager | Key Vault | Secret Manager |
| schedule | EventBridge (6-field cron, rate) | Container Apps Job (cron) | Cloud Scheduler (cron only) |
| relationship enforcement | security group rule | **none** | IAM invoker binding |
| cdn + waf | 2 resources | 5 resources | 7 resources |
| size ceiling | none reached | 2 vCPU / 4 GiB | 8 vCPU |

## Recommended actions

> **Status (2026-08-07): none of items 1-5 below were implemented.**
> GCP shipped by reusing the existing model largely as-is rather than
> acting on this list — see the per-item annotations above for specifics.
> This list remains a legitimate backlog if GCP support gets hardened
> further (per `plan.md`'s note that GCP verification is intentionally
> lighter than AWS/Azure's).

**Revisions to earlier conclusions:**

1. Keep `Relationship` as a security primitive; qualify the docstring to say
   enforcement is per-target and best-effort. (Reverses the Azure verdict.)
2. Move `log_retention_days` from `BaseEnvironment` to `AwsEnvironment`. Two of
   three targets ignore it.

**New work, in priority order:**

3. Replace endpoint injection by name-matching with a structured connection
   descriptor per managed capability. This is now supported by evidence from
   two clouds and from the ecs_composex comparison.
4. Give backends a way to reject what they cannot express — an unrenderable
   `RateSchedule`, a `size` above the target's ceiling — with an error naming
   the target and the limit.
5. Add a `domain` to the CDN surface, since one target cannot issue a
   certificate without it.

**Still explicitly not needed:** no new capabilities. Three clouds, and
`container` / `database` / `cache` / `object-storage` has not needed extending.
That is the strongest evidence so far that the top of the model is right.
