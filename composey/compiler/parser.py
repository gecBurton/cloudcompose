import json
import os
import subprocess

import yaml

from ..models.compose import Application as DockerApplication


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

    if "services" in raw_yaml:
        for s_name, s_data in raw_yaml["services"].items():
            if "x-composey" in s_data and s_name in raw["services"]:
                raw["services"][s_name]["x-composey"] = s_data["x-composey"]

    return DockerApplication.model_validate(raw)
