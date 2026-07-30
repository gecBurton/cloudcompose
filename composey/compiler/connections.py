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
from dataclasses import dataclass
from typing import Mapping, Optional

from ..models.semantic import Connection


@dataclass(frozen=True)
class Resolution:
    """
    What one environment variable resolved to, and what that implies.

    A resolved value is no longer just a string: pointing a client at a managed
    service can hand it a credential, and a credential cannot travel as a plain
    environment variable. The caller needs to know which, so the fact is
    reported here rather than re-derived by inspecting the value for something
    password-shaped.
    """

    value: str
    service: Optional[str] = None
    confidential: bool = False


def _url_pattern(service_name: str) -> re.Pattern:
    """Match a URL whose host is exactly `service_name`."""
    return re.compile(
        r"^(?P<scheme>[A-Za-z][A-Za-z0-9+.\-]*)://"
        r"(?P<userinfo>[^@/?#]*@)?"
        rf"{re.escape(service_name)}"
        r"(?::(?P<port>\d+))?"
        r"(?P<path>/[^?#]*)?"
        r"(?P<query>[?#].*)?$"
    )


def _userinfo(stated: Optional[str], connection: Connection) -> str:
    """
    The credentials a client presents, once the managed service has replaced
    the container.

    The connection is authoritative, for the same reason it is about the port:
    the username a compose file wrote belonged to a container the platform threw
    away, and the managed service generated its own. Preserving what was written
    locally produces a URL that resolves to a real database and is rejected by
    it.
    """
    if connection.username is None:
        return stated or ""

    if connection.password is None:
        return f"{connection.username}@"
    return f"{connection.username}:{connection.password}@"


def _path(stated: Optional[str], connection: Connection) -> str:
    """
    The path component, which for a database URL names the database.

    Substituted for the same reason as the credentials: the name in the compose
    file is the one the local container created, and the managed instance holds
    whatever the compiler asked for.
    """
    if connection.database is not None:
        return f"/{connection.database}"
    return stated or ""


def _rebuild_url(match: re.Match, connection: Connection) -> str:
    """Swap a URL's host, credentials and database for the managed service's."""
    # The connection is authoritative about the port: a managed service rarely
    # listens where the local container did. No port means the scheme's default
    # applies, so none is written.
    port = f":{connection.port}" if connection.port is not None else ""
    return (
        f"{match.group('scheme')}://"
        f"{_userinfo(match.group('userinfo'), connection)}"
        f"{connection.host}{port}"
        f"{_path(match.group('path'), connection)}"
        f"{match.group('query') or ''}"
    )


def resolve_value(value: str, connections: Mapping[str, Connection]) -> Resolution:
    """
    Resolve one environment variable value against the services it references.

    Returns the value unchanged, referencing nothing, when it refers to no
    managed service.
    """
    for service_name, connection in connections.items():
        if value == service_name:
            # A bare reference is an address or an identifier, never a
            # credential, so it stays an ordinary environment variable.
            return Resolution(connection.bare_reference, service=service_name)

        match = _url_pattern(service_name).match(value)
        if match:
            return Resolution(
                _rebuild_url(match, connection),
                service=service_name,
                confidential=connection.password is not None,
            )

    return Resolution(value)


def default_port(connection: Optional[Connection], fallback: int) -> int:
    """The port a client reaches a service on, for firewall-style rules."""
    if connection is None or connection.port is None:
        return fallback
    return connection.port
