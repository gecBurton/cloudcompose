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
    decisions = [
        Decision(
            name,
            f"volume {volume!r} becomes object storage",
            "named volume",
            "inferred",
        )
        for volume in service.storage
    ]

    declared = len(docker_service.volumes or [])
    if declared > len(service.storage):
        decisions.append(
            Decision(
                name,
                f"{declared - len(service.storage)} mount(s) dropped",
                "bind mounts and anonymous volumes have no deployed meaning",
                "warning",
            )
        )
    return decisions


def _wiring_decisions(
    name: str, semantic: SemanticApplication, service
) -> list[Decision]:
    """Report whether this service's references to managed services resolve."""
    decisions: list[Decision] = []
    managed = {
        s.name: s
        for s in semantic.services
        if s.capability != "container"
        and any(r.client == name and r.server == s.name for r in semantic.relationships)
    }

    for server_name in managed:
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
    docker_app: DockerApplication, semantic: SemanticApplication
) -> list[Decision]:
    """Describe every inference made while normalizing this application."""
    decisions: list[Decision] = []
    by_name = {s.name: s for s in semantic.services}

    for name, docker_service in docker_app.services.items():
        service = by_name[name]
        raw = docker_service.x_composey_raw

        decisions.append(_capability_decision(name, service, "capability" in raw))

        if service.capability != "container":
            continue

        decisions.extend(_port_decisions(name, docker_service, service))

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

        decisions.extend(_volume_decisions(name, docker_service, service))
        decisions.extend(_wiring_decisions(name, semantic, service))

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


def _ingress_decisions(
    docker_app: DockerApplication, semantic: SemanticApplication
) -> list[Decision]:
    public = semantic.public_services
    if public:
        decisions = []
        for service in public:
            # Exposure can only be declared, so the interesting distinction is
            # which parts of the route were spelled out and which took defaults.
            spelled_out = (
                docker_app.services[service.name].x_composey_raw.get("ingress") or {}
            )
            decisions.append(
                Decision(
                    service.name,
                    f"served at {service.ingress.path} on port {service.ingress.port}",
                    "declared by x-composey: ingress",
                    "declared",
                )
            )
            decisions.append(
                Decision(
                    service.name,
                    f"healthy when {service.ingress.health_path} returns 2xx/3xx",
                    "declared"
                    if "health_path" in spelled_out
                    else "default health path — set ingress.health_path if wrong",
                    "declared" if "health_path" in spelled_out else "default",
                )
            )
        return decisions

    published = [
        name
        for name, service in docker_app.services.items()
        if any(p.published for p in service.ports or [])
    ]
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
