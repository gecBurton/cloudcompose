"""
Reporting every decision the compiler made, and why.

Inference is only safe when a wrong guess is visible. Most of composey's
failures on real compose files were silent: a database deployed as a container,
an application with no ingress at all, a bind mount quietly dropped. Each of
those produced Terraform that applied cleanly and did not work.

This turns those into something readable before anything is deployed. It
reports what was decided, what it was decided from, and — most importantly —
the places where nothing was decided at all.
"""

from dataclasses import dataclass
from typing import Literal

from ..models.compose import Application as DockerApplication
from ..models.semantic import Application as SemanticApplication
from ..models.semantic import CronSchedule
from .connections import _url_pattern

Source = Literal["declared", "inferred", "default", "warning"]


@dataclass(frozen=True)
class Decision:
    """One choice the compiler made about one subject."""

    subject: str
    decision: str
    because: str
    source: Source


def _capability_decision(name: str, service, declared: bool) -> Decision:
    """
    Report what a service was treated as.

    The wording deliberately does not change with the source: the same outcome
    should read the same whether it was declared or guessed, so that scanning
    the report for what happened is separate from asking why.
    """
    outcome = (
        "runs as a container"
        if service.capability == "container"
        else f"substituted for a managed {service.capability}"
    )

    if declared:
        return Decision(name, outcome, "declared by x-composey: capability", "declared")

    because = (
        f"image {service.image!r} is not a recognised managed service"
        if service.capability == "container"
        else f"image {service.image!r} is a recognised {service.capability}"
    )
    return Decision(name, outcome, because, "inferred")


def _port_decisions(name: str, docker_service, service) -> list[Decision]:
    decisions: list[Decision] = []
    ports = docker_service.ports or []

    if service.port is None:
        return decisions

    decisions.append(
        Decision(name, f"listens on {service.port}", "first published port", "inferred")
    )
    if len(ports) > 1:
        ignored = ", ".join(str(p.target) for p in ports[1:])
        decisions.append(
            Decision(
                name,
                f"ports {ignored} are not exposed",
                "only the first port of a service is used",
                "warning",
            )
        )
    return decisions


def _volume_decisions(name: str, docker_service, service) -> list[Decision]:
    """Named volumes are rejected before this runs, so any left are local-only."""
    dropped = len(docker_service.volumes or [])
    if not dropped:
        return []

    return [
        Decision(
            name,
            f"{dropped} mount(s) dropped",
            "bind mounts and anonymous volumes have no deployed meaning",
            "warning",
        )
    ]


def _wiring_decisions(
    name: str, semantic: SemanticApplication, service
) -> list[Decision]:
    """Report whether this service's references to its dependencies resolve."""
    decisions: list[Decision] = []
    servers = {
        s.name: s
        for s in semantic.services
        if s.name != name
        and any(r.client == name and r.server == s.name for r in semantic.relationships)
    }

    for server_name, server in servers.items():
        # A container with no port has no address to hand out.
        if (
            server.capability == "container"
            and server.port is None
            and any(
                value == server_name or _url_pattern(server_name).match(value)
                for value in service.env.values()
            )
        ):
            decisions.append(
                Decision(
                    name,
                    f"cannot reach {server_name}",
                    f"{server_name} publishes no port, so it has no address to "
                    f"be found at",
                    "warning",
                )
            )
            continue

        matched = [
            key
            for key, value in service.env.items()
            if value == server_name or _url_pattern(server_name).match(value)
        ]

        if matched:
            decisions.append(
                Decision(
                    name,
                    f"{', '.join(sorted(matched))} → {server_name}",
                    f"value references {server_name!r}",
                    "inferred",
                )
            )
        else:
            decisions.append(
                Decision(
                    name,
                    f"nothing wired to {server_name}",
                    f"no environment variable references {server_name!r}; the "
                    f"service will not be able to find it",
                    "warning",
                )
            )
    return decisions


def explain(
    docker_app: DockerApplication | None, semantic: SemanticApplication
) -> list[Decision]:
    """
    Describe every inference made while normalizing this application.

    docker_app is optional: only a handful of decisions below need the raw
    compose model specifically (whether `capability` was declared verbatim
    rather than inferred, the exact list of dropped ports/mounts, and the
    fallback "which services publish a port" detail when nothing is public).
    Everything else — schedule, scaling, CDN, size, wiring, platform config,
    empty secrets, declared ingress, relationships — is answerable from the
    semantic model alone, which the Go parser/normalizer always produces.
    Skipping all of it whenever docker_app is unavailable would throw away
    everything explain() exists for on exactly the path that has no Python
    parser to fall back to.
    """
    decisions: list[Decision] = []

    for service in semantic.services:
        name = service.name
        declared_capability = (
            "capability" in docker_app.services[name].x_composey_raw
            if docker_app is not None
            # Without the raw compose model there is no direct record of
            # whether `capability` was written explicitly. Whether the
            # inferred value would have guessed the same capability from the
            # image name is the closest available proxy: if it disagrees,
            # something must have overridden it.
            else service.capability != _infer_capability_reference(service.image)
        )
        decisions.append(_capability_decision(name, service, declared_capability))

        if service.capability != "container":
            continue

        if docker_app is not None:
            decisions.extend(_port_decisions(name, docker_app.services[name], service))
        elif service.port is not None:
            decisions.append(
                Decision(
                    name,
                    f"listens on {service.port}",
                    "first published port",
                    "inferred",
                )
            )

        if service.build_context:
            dockerfile = service.dockerfile or "Dockerfile"
            decisions.append(
                Decision(
                    name,
                    "built from source and pushed to a registry",
                    f"build context {service.build_context!r}, {dockerfile}",
                    "inferred",
                )
            )

        if service.schedule:
            shape = (
                f"cron {service.schedule.expression!r}"
                if isinstance(service.schedule, CronSchedule)
                else f"every {service.schedule.value} {service.schedule.unit}"
            )
            decisions.append(
                Decision(
                    name,
                    "runs as a scheduled task, not a long-running service",
                    f"schedule: {shape}",
                    "declared",
                )
            )

        if service.max_scale > 1:
            decisions.append(
                Decision(
                    name,
                    f"scales between {service.min_scale} and {service.max_scale}",
                    "max_scale is greater than one",
                    "declared",
                )
            )

        if service.cdn_enabled:
            decisions.append(
                Decision(name, "fronted by a CDN with a WAF", "cdn: true", "declared")
            )

        if service.size != "small" or service.cpu or service.memory:
            decisions.append(
                Decision(
                    name,
                    f"size {service.size}",
                    "declared by x-composey",
                    "declared",
                )
            )

        if docker_app is not None:
            decisions.extend(
                _volume_decisions(name, docker_app.services[name], service)
            )
        decisions.extend(_wiring_decisions(name, semantic, service))

        if service.config:
            decisions.append(
                Decision(
                    name,
                    f"{len(service.config)} variable(s) need values from the platform",
                    f"{', '.join(service.config[:4])}"
                    + (
                        f" and {len(service.config) - 4} more"
                        if len(service.config) > 4
                        else ""
                    )
                    + " come from env_file or ${...}, so the compose file names "
                    "them but does not value them; set them in Secrets Manager "
                    "before the service will work",
                    "warning",
                )
            )

        for secret in service.secrets:
            decisions.append(
                Decision(
                    name,
                    f"secret {secret!r} created empty",
                    "the value must be set out of band before the service works",
                    "warning",
                )
            )

    decisions.extend(_ingress_decisions(docker_app, semantic))

    for relationship in semantic.relationships:
        decisions.append(
            Decision(
                relationship.client,
                f"may connect to {relationship.server}",
                "depends_on",
                "inferred",
            )
        )

    return decisions


def _infer_capability_reference(image: str) -> str:
    """
    A standalone copy of what the normalizer's own capability inference
    would have guessed from the image name alone, used only as a proxy for
    "was this declared rather than inferred" when there is no raw compose
    model to check directly (the Go-parser path). Kept intentionally
    separate from any single canonical implementation: normalizer.py, the
    Python one this was ported from, no longer exists on this branch, and
    the Go implementation lives in a different language entirely.
    """
    from ..constants import CAPABILITY_IMAGES

    reference = image.lower().split("@")[0]
    path, _, last = reference.rpartition("/")
    segments = f"{path}/{last.split(':')[0]}".strip("/").split("/")

    if len(segments) > 1 and ("." in segments[0] or segments[0] == "localhost"):
        segments = segments[1:]

    for capability, known in CAPABILITY_IMAGES.items():
        if any(segment in known for segment in segments):
            return capability

    return "container"


def _ingress_decisions(
    docker_app: DockerApplication | None, semantic: SemanticApplication
) -> list[Decision]:
    public = semantic.public_services
    if public:
        decisions = []
        for service in public:
            decisions.append(
                Decision(
                    service.name,
                    f"served at {service.ingress.path} on port {service.ingress.port}",
                    "declared by x-composey: ingress",
                    "declared",
                )
            )
            health_path = (
                service.ingress.health_check.path
                if service.ingress.health_check
                else "/"
            )
            decisions.append(
                Decision(
                    service.name,
                    f"healthy when {health_path} returns 2xx/3xx",
                    "declared"
                    if service.ingress.health_check
                    else "default health path — set health_check.path if wrong",
                    "declared" if service.ingress.health_check else "default",
                )
            )
        return decisions

    if docker_app is not None:
        published = [
            name
            for name, service in docker_app.services.items()
            if any(p.published for p in service.ports or [])
        ]
    else:
        # Without the raw compose model, a service's own resolved port is
        # the closest available signal for "this looked like it wanted to
        # be reached" — it is not exactly "published a port" (a compose
        # file can list ports without ever publishing one), so this errs
        # towards naming a candidate rather than staying silent.
        published = [s.name for s in semantic.services if s.port is not None]
    because = (
        f"{', '.join(published)} publish ports; declare x-composey: ingress on "
        f"whichever should be reachable from outside"
        if published
        else "no service publishes a port"
    )
    return [Decision("application", "NOT reachable from outside", because, "warning")]


def render(decisions: list[Decision]) -> str:
    """Render decisions as grouped, readable text."""
    lines: list[str] = []
    marks = {
        "declared": "[cyan]declared[/]",
        "inferred": "[green]inferred[/]",
        "default": "[dim]default [/]",
        "warning": "[yellow]warning [/]",
    }

    for subject in dict.fromkeys(d.subject for d in decisions):
        lines.append(f"\n[bold]{subject}[/]")
        for decision in (d for d in decisions if d.subject == subject):
            lines.append(f"  {marks[decision.source]}  {decision.decision}")
            lines.append(f"            [dim]{decision.because}[/]")

    warnings = sum(1 for d in decisions if d.source == "warning")
    lines.append(
        f"\n{len(decisions)} decision(s), [yellow]{warnings} worth checking[/]"
        if warnings
        else f"\n{len(decisions)} decision(s)"
    )
    return "\n".join(lines)
