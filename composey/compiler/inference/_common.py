"""Common inference utilities.

Shared helper functions used across inference modules.
"""

import hashlib
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from composey.models.semantic import Application as SemanticApp


def safe_terraform_identifier(name: str) -> str:
    """Convert a name to a Terraform-safe identifier fragment."""
    return "".join(c if c.isalnum() else "_" for c in name).strip("_")


def namespace_for(env_name: str, app_name: str) -> str:
    """Create a private DNS zone name for this application.

    Scoped by environment and application because Cloud Map namespaces are
    unique per VPC, and several applications routinely share one.
    """
    label = "-".join(part for part in (env_name, app_name) if part)
    return f"{''.join(c if c.isalnum() or c == '-' else '-' for c in label).strip('-').lower()}.internal"


def calculate_listener_priorities(app: "SemanticApp") -> dict[str, int]:
    """Assign each public service a stable, unique listener rule priority.

    Listener rule priorities must be unique across every application sharing a
    listener, so they cannot simply start at 1. Each application gets a band
    derived from its name, and its routes are ordered within that band by path
    specificity, longest first, so that /api/admin is matched before /api.
    """
    band = _priority_band(app.name)
    ordered = sorted(app.public_services, key=lambda s: (-len(s.ingress.path), s.name))

    priorities: dict[str, int] = {}
    for offset, service in enumerate(ordered):
        priorities[service.name] = (
            service.ingress.priority
            if service.ingress.priority is not None
            else band + offset
        )
    return priorities


def _priority_band(app_name: str) -> int:
    """Calculate the priority band for an application."""
    from composey.constants import BAND_WIDTH, PRIORITY_BANDS

    digest = hashlib.sha256(app_name.encode()).digest()
    return 1 + (int.from_bytes(digest[:4], "big") % PRIORITY_BANDS) * BAND_WIDTH


def path_patterns(path: str) -> list[str]:
    """Generate ALB path patterns matching a prefix and everything beneath it."""
    if path == "/":
        return ["/*"]
    trimmed = path.rstrip("/")
    return [trimmed, f"{trimmed}/*"]
