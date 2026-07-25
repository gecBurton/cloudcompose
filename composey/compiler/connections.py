"""
Rewriting a client's environment to point at managed services.

When composey substitutes a managed service for a container, every client that
referred to it by its compose name has to be pointed at the real thing instead.
The compose file already records which variables those are, in their *values*:
`DB_HOST: db` names the service, and `REDIS_URL: redis://cache:6379` embeds it.
Resolution is driven entirely by those references.

Variable *names* are deliberately not consulted. Guessing from a name — that
anything ending `_BUCKET` wants a bucket, that anything containing `URL` wants
an endpoint — misfires on variables that were never about the service at all,
and encodes one cloud's idea of what a connection looks like. The backend says
what the attributes are; this module only decides where they go.
"""

import re
from typing import Mapping, Optional

from ..models.semantic import Connection


def _url_pattern(service_name: str) -> re.Pattern:
    """Match a URL whose host is exactly `service_name`."""
    return re.compile(
        r"^(?P<scheme>[A-Za-z][A-Za-z0-9+.\-]*)://"
        r"(?P<userinfo>[^@/?#]*@)?"
        rf"{re.escape(service_name)}"
        r"(?::(?P<port>\d+))?"
        r"(?P<rest>[/?#].*)?$"
    )


def _rebuild_url(match: re.Match, connection: Connection) -> str:
    """Swap a URL's host for the managed service's real address."""
    # The connection is authoritative about the port: a managed service rarely
    # listens where the local container did. No port means the scheme's default
    # applies, so none is written.
    port = f":{connection.port}" if connection.port is not None else ""
    return (
        f"{match.group('scheme')}://"
        f"{match.group('userinfo') or ''}"
        f"{connection.host}{port}"
        f"{match.group('rest') or ''}"
    )


def resolve_value(value: str, connections: Mapping[str, Connection]) -> str:
    """
    Resolve one environment variable value against the services it references.

    Returns the value unchanged when it refers to no managed service.
    """
    for service_name, connection in connections.items():
        if value == service_name:
            return connection.bare_reference

        match = _url_pattern(service_name).match(value)
        if match:
            return _rebuild_url(match, connection)

    return value


def resolve_environment(
    environment: list[dict], connections: Mapping[str, Connection]
) -> list[dict]:
    """Resolve a container's environment list in place-equivalent fashion."""
    return [
        {**entry, "value": resolve_value(entry["value"], connections)}
        for entry in environment
    ]


def default_port(connection: Optional[Connection], fallback: int) -> int:
    """The port a client reaches a service on, for firewall-style rules."""
    if connection is None or connection.port is None:
        return fallback
    return connection.port
