import json
import os
import subprocess

import yaml

from ..models.compose import Application as DockerApplication


def _declared_environment(service: dict) -> dict:
    """The environment a service writes in the compose file itself."""
    declared = service.get("environment") or {}
    if isinstance(declared, list):
        pairs = (str(entry).split("=", 1) for entry in declared)
        return {pair[0]: pair[1] if len(pair) > 1 else None for pair in pairs}
    return declared


def _split_environment(resolved: dict, declared_service: dict) -> None:
    """
    Separate values the compose file states from values it merely names.

    `docker compose config` folds env_file contents and ${VAR} substitutions into
    the environment, so a resolved value may have come from a developer's local
    .env. Those must not cross into the deployment. They are frequently secret --
    passwords, tokens, DSNs -- and where they are not they are usually wrong
    anyway: the same files supply ENVIRONMENT=local and POSTGRES_HOST=localhost.

    A value crosses over only when written literally in the compose file, which
    is committed and therefore not secret. Everything else contributes its
    *name*: the application needs that variable, and the platform supplies the
    value.
    """
    declared = _declared_environment(declared_service)
    literal = {
        key
        for key, value in declared.items()
        if value is not None and "${" not in str(value)
    }

    environment = resolved.get("environment") or {}
    resolved["environment"] = {k: v for k, v in environment.items() if k in literal}
    resolved["platform_env"] = sorted(set(environment) - literal)


def parse(file_path: str, level=2) -> DockerApplication:
    # 1. Use docker compose config to handle interpolation and normalization
    result = subprocess.run(
        ["docker", "compose", "-f", file_path, "config", "--format", "json"],
        capture_output=True,
        text=True,
        check=True,
    )
    raw = json.loads(result.stdout)

    # `docker compose config` resolves build contexts to absolute paths on the
    # machine doing the compiling. Re-root them on the compose file so the
    # generated Terraform neither leaks local paths nor depends on where the
    # repository happens to sit.
    compose_dir = os.path.dirname(os.path.abspath(file_path))
    for service in raw.get("services", {}).values():
        build = service.get("build")
        if isinstance(build, dict) and os.path.isabs(build.get("context", "")):
            build["context"] = os.path.relpath(build["context"], compose_dir)

    # 2. Extract x-composey extensions from raw YAML (docker-compose strips them)
    with open(file_path, "r") as f:
        raw_yaml = yaml.safe_load(f)

    for s_name, s_data in (raw_yaml.get("services") or {}).items():
        if s_name not in raw["services"]:
            continue
        if "x-composey" in s_data:
            raw["services"][s_name]["x-composey"] = s_data["x-composey"]
        _split_environment(raw["services"][s_name], s_data)

    return DockerApplication.model_validate(raw)
