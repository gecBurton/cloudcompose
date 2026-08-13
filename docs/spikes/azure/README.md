# Spike: can the semantic model express Azure? (historical)

Design spike from before Azure was implemented (no compiler code, nothing
deployed) — used to stress-test whether the semantic model
(`container`/`database`/`cache`/`object-storage`, `size`, `min_scale`,
`cdn`, etc.) could express Azure's shape before committing to a real
backend.

**Verdict at the time**: the capability vocabulary held with no new
capabilities needed, but five things didn't survive contact — networking
enforcement (`Relationship` had no Azure equivalent to AWS's security
groups), `schedule`'s EventBridge-specific cron syntax, `size: large`
exceeding Container Apps' Consumption tier, `health_check_grace_period`
being an ECS-only concept, and `public_service` assuming a shared ALB.

All five have since been addressed in the real implementation — see
`../../azure-aws-parity-todo.md` for current status and
`../../azure-app-isolation-design.md` for the networking-enforcement
resolution specifically. Kept here only as the original design-time
record.
