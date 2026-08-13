# Spike: can the semantic model express GCP? (historical)

Companion to `../azure/README.md`, same method: design-time spike before
GCP was implemented. GCP has since been implemented with deliberately
lighter verification than AWS/Azure — it has never been tested against a
real deployment (see `../../azure-aws-parity-todo.md` for current
status).

**Verdict at the time**: the capability vocabulary held for a third
cloud. Notably, GCP *does* have a per-pair enforcement point for
`Relationship` (`roles/run.invoker` IAM bindings) — the opposite finding
from the Azure spike, which found no equivalent at the time. Other
findings — `RateSchedule` not always renderable on Cloud Scheduler
(cron-only, no rate concept), Cloud SQL's idiomatic connection path being
a unix socket rather than host:port, `cdn: true` needing a `domain` GCP
has no free hostname equivalent for — remain partially open; see
`../../azure-aws-parity-todo.md`'s GCP section.

## Three-cloud comparison (resources emitted for `examples/hello`)

| | AWS | Azure | GCP |
| --- | --- | --- | --- |
| resources | 11 | 1 | 2 |
| compute | ECS Fargate | Container Apps | Cloud Run |
| ingress | ALB + TG + listener rule + SG | built-in | built-in + IAM invoker |
| identity | task role + exec role | managed identity | service account |
| access control | synthesised IAM policy | built-in RBAC role | predefined role |
| database | RDS | Flexible Server | Cloud SQL |
| cache | ElastiCache | Cache for Redis | Memorystore |
| object storage | S3 | Blob container | GCS bucket |
| secrets | Secrets Manager | Key Vault | Secret Manager |
| schedule | EventBridge (6-field cron, rate) | Container Apps Job (cron) | Cloud Scheduler (cron only) |
| relationship enforcement | security group rule | Container Apps Environment (see `../../azure-app-isolation-design.md`) | IAM invoker binding |
| cdn + waf | 2 resources | 5 resources | 7 resources |
| size ceiling | none reached | 2 vCPU / 4 GiB | 8 vCPU |
