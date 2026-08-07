# The Serverless Debate (2026-08-01): what we considered, and why we didn't build it

This is a condensed historical record of a same-day (2026-08-01) design debate,
originally spread across five separate documents, that took place shortly
after GCP support was first added and well before the Python→Go migration
(see `plan.md`). It's kept because the debate raised real, still-relevant
technical tradeoffs, even though its proposed solution was never built.

## The question

GCP's Cloud Run model (scale-to-zero, request-based billing, fast cold
starts, built-in ingress) looked structurally simpler than composey's
existing AWS (ECS Fargate + ALB) and Azure approaches. The question raised
was whether to redesign the whole semantic model around a serverless-first,
multi-type service abstraction — designing for GCP's model first and
backporting it to AWS/Azure — rather than keeping one uniform container
model across all three clouds.

## The proposal that was considered

Replace the existing `min_scale`/`max_scale` capacity model with an explicit
`type:` discriminator per service:

```yaml
services:
  api:
    x-composey:
      type: http_service       # scale-to-zero, request-based
  worker:
    x-composey:
      type: event_processor    # queue-triggered, scale-to-zero
  cleanup:
    x-composey:
      type: scheduled_task     # cron, scale-to-zero between runs
  websocket:
    x-composey:
      type: persistent_service # always-on escape hatch, current behavior
```

Each type would map to a different resource shape per cloud (Cloud Run /
Container Apps+KEDA / Lambda-or-Fargate+EventBridge for AWS's harder case),
with a 10-week phased rollout: Azure first (closest to the target model),
then GCP, then an AWS refactor last.

## The strongest argument against it

A companion analysis broke down how much of the "cloud-native app" space
this model would actually cover well:

| Category | Fit | Est. share |
|---|---|---|
| HTTP APIs, background workers, scheduled tasks | Good | ~70% |
| WebSockets/real-time, stateful services, legacy apps, HPC | Poor/excluded | ~30% |

The critical failure mode: WebSocket and other persistent-connection
workloads don't fit any serverless HTTP-handler type — request timeouts
(60 min max even on Cloud Run) forcibly close connections, and scale-to-zero
is fundamentally incompatible with an open socket. The proposed workaround
was a `persistent_service` escape-hatch type for these cases, which the
analysis itself pointed out gives up the entire cost/simplicity benefit
serverless was supposed to provide, for exactly the workloads composey's own
early examples (chat/realtime apps) needed.

A separate deep-dive on GCP specifically confirmed the underlying platform
facts, which remain true today regardless of what composey does: Cloud Run
supports WebSockets, but only as long-running HTTP requests bounded by the
request timeout, with no shared state across instances (multi-instance chat
rooms need an external broker like Redis pub/sub), no scale-to-zero while a
socket is open, and only best-effort session affinity. AWS ALB and Azure
Container Apps have the identical multi-instance/no-scale-to-zero-while-open
constraint — this isn't a GCP limitation, it's inherent to any
horizontally-scaled stateless compute model.

A third framing pushed further and asked whether WebSockets should be
dropped from the product entirely ("socketless"), pointing users toward
SSE/polling or third-party realtime SaaS (Pusher/Ably) instead, to make the
whole platform uniformly serverless. This was the most speculative version
of the debate and was not pursued.

## What was actually decided

None of the proposed `ServiceType` taxonomy was implemented. The semantic
model kept (and still has, per `internal/models/semantic.go` and
`AGENTS.md`) one capability-based `Service` model — `container`, `database`,
`cache`, `object-storage` — with `min_scale`/`max_scale` as the only capacity
knobs, deployed identically in shape across ECS Fargate, Container Apps, and
Cloud Run. There is no `type: http_service`/`persistent_service` split, no
`websocket: true` flag, and no automatic Redis-broker wiring.

WebSockets work today without any special-casing, because all three compute
targets (ECS Fargate, Container Apps, Cloud Run) are container/request-based
runtimes that hold persistent connections natively — the "escape hatch" this
debate worried about needing is just the only mode that ever existed. The
tradeoff the debate anticipated (serverless simplicity vs. WebSocket support)
turned out to be a false choice once the decision was made not to chase
Cloud-Run-style scale-to-zero as a design goal in the first place.

## If this comes up again

The technical facts in this doc (multi-instance state sharing needing an
external broker, request-timeout limits, scale-to-zero being incompatible
with open connections) are still accurate platform behavior on all three
clouds and worth re-reading before re-opening this question. The market/UX
argument (serverless-first covers ~70% of workloads well, excludes the rest)
is the strongest case *for* revisiting this — but doing so would mean
re-litigating a decision that's now load-bearing throughout
`internal/compiler/{aws,azure,gcp}`'s current inference code, not a small
tweak.
