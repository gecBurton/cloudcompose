"""
Azure resource naming.

Azure constrains names per resource type, and the constraints disagree with
each other: a container registry takes alphanumerics only, a storage account
takes lowercase alphanumerics, a key vault takes dashes too. All three are
globally unique across Azure and capped well below the length of a readable
`{environment}-{application}-{resource}` name.

Applying a fixup at each call site is what produced names Azure rejected —
`prod-flask-acr` for a registry, `prod-nginx-flask-mysql-kv` at 25 characters
for a key vault — so the rules live here instead, one function per resource
type, and callers do not post-process what they return.

When a name is too long it is truncated and given a short digest of the full
name. Plain truncation would silently collide: `nginx-flask-mysql` and
`nginx-flask-mysqlx` share a prefix and would otherwise land on the same key
vault, with the second deployment failing against the first's resource. Names
that already fit are left alone, so the common case stays readable.
"""

import hashlib
import re

_DIGEST_LEN = 6


def _digest(value: str) -> str:
    """Short, stable discriminator for names that have to be truncated."""
    return hashlib.sha256(value.encode()).hexdigest()[:_DIGEST_LEN]


def _fit(candidate: str, full: str, min_len: int, max_len: int, sep: str = "") -> str:
    """Force `candidate` within Azure's length bounds without losing uniqueness."""
    if len(candidate) > max_len:
        keep = max_len - _DIGEST_LEN - len(sep)
        candidate = candidate[:keep].rstrip("-") + sep + _digest(full)
    if len(candidate) < min_len:
        candidate = (candidate + _digest(full))[:max_len]
    return candidate


def container_registry_name(env_name: str, app_name: str) -> str:
    """
    Name for azurerm_container_registry: 5-50 alphanumeric characters.

    Dashes and underscores are not merely discouraged here, they are rejected:
    "alpha numeric characters only are allowed in name".
    """
    full = f"{env_name}-{app_name}-acr"
    candidate = re.sub(r"[^a-zA-Z0-9]", "", full)
    return _fit(candidate, full, min_len=5, max_len=50)


def storage_account_name(env_name: str, app_name: str, service_name: str) -> str:
    """Name for azurerm_storage_account: 3-24 lowercase alphanumeric characters."""
    full = f"{env_name}-{app_name}-{service_name}"
    candidate = re.sub(r"[^a-z0-9]", "", full.lower())
    return _fit(candidate, full, min_len=3, max_len=24)


def key_vault_name(env_name: str, app_name: str) -> str:
    """
    Name for azurerm_key_vault: 3-24 characters, alphanumerics and dashes,
    starting with a letter and not ending with one.
    """
    full = f"{env_name}-{app_name}-kv"
    candidate = re.sub(r"[^a-zA-Z0-9-]", "-", full)
    candidate = re.sub(r"-+", "-", candidate).strip("-")
    if not candidate[:1].isalpha():
        candidate = f"kv-{candidate}"
    return _fit(candidate, full, min_len=3, max_len=24, sep="-").strip("-")


def frontdoor_profile_name(env_name: str, app_name: str) -> str:
    """Name for azurerm_cdn_frontdoor_profile: no documented length cap below
    260 characters, but kept consistent with the rest of this module's
    dash-joined, digest-on-collision convention rather than relying on that."""
    full = f"{env_name}-{app_name}-fd"
    candidate = re.sub(r"[^a-zA-Z0-9-]", "-", full)
    candidate = re.sub(r"-+", "-", candidate).strip("-")
    return _fit(candidate, full, min_len=1, max_len=64, sep="-").strip("-")
