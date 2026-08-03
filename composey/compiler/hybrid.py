"""
Hybrid compiler: Uses Go for parser/normalizer, Python for inference/generation.

This allows incremental migration:
- Phase 2 (now): Replace parser + normalizer with Go
- Phase 3 (later): Replace AWS inference + generator with Go
- Phase 4 (later): Replace Azure inference + generator with Go
"""

import json
import subprocess
from pathlib import Path

from composey.compiler.generator import generate as generate_aws
from composey.compiler.generator_azure import generate as generate_azure
from composey.compiler.generator_gcp import generate as generate_gcp
from composey.compiler.inference import infer as infer_aws
from composey.compiler.inference.azure import infer as infer_azure
from composey.compiler.inference.gcp import infer as infer_gcp
from composey.models.environment import (
    AwsEnvironment,
    AzureEnvironment,
    BaseEnvironment,
    GcpEnvironment,
)
from composey.models.semantic import Application as SemanticApplication


def parse_and_normalize_go(compose_file: str, project_name: str) -> SemanticApplication:
    """
    Use Go binary for parsing and normalization.

    This replaces Python's parse() + normalize() with a single call to the Go binary.
    """
    go_binary = Path(__file__).parent.parent.parent / "composey-go" / "composey-go"

    if not go_binary.exists():
        raise RuntimeError(
            f"Go binary not found at {go_binary}. "
            f"Run: cd composey-go && go build -o composey-go ./cmd/composey"
        )

    result = subprocess.run(
        [str(go_binary), "normalize", compose_file],
        capture_output=True,
        text=True,
        check=True,
    )

    # Parse JSON into dict
    data = json.loads(result.stdout)

    # Convert dict to SemanticApplication Pydantic model
    # This validates the Go output is correct
    return SemanticApplication(**data)


def compile_application_hybrid(
    app: SemanticApplication, env: BaseEnvironment, use_go_frontend: bool = True
) -> str:
    """
    Hybrid compilation: Go frontend (parser + normalizer), Python backend (inference + generation).

    Args:
        app: Semantic application model
        env: Environment configuration (AWS/Azure/GCP)
        use_go_frontend: If True, use Go for parser+normalizer. If False, use Python.

    This allows gradual migration by toggling use_go_frontend.
    """
    if isinstance(env, AwsEnvironment):
        return generate_aws(infer_aws(app, env), env)
    elif isinstance(env, AzureEnvironment):
        return generate_azure(infer_azure(app, env), env)
    elif isinstance(env, GcpEnvironment):
        return generate_gcp(infer_gcp(app, env), env)
    else:
        raise NotImplementedError(
            f"No compiler backend is implemented for target {env.target!r}"
        )


def compile_to_terraform_hybrid(
    compose_file: str,
    env: BaseEnvironment,
    project_name: str,
    use_go_frontend: bool = True,
) -> str:
    """
    Compile compose file to Terraform using hybrid approach.

    Args:
        compose_file: Path to docker-compose.yml
        env: Environment configuration
        project_name: Project name for resource naming
        use_go_frontend: Use Go for parser+normalizer (default: True)

    Returns:
        Terraform JSON string
    """
    if use_go_frontend:
        app = parse_and_normalize_go(compose_file, project_name)
    else:
        # Fallback to Python (for comparison testing)
        from composey.compiler.normalizer import normalize
        from composey.compiler.parser import parse

        app = normalize(parse(compose_file), project_name)

    return compile_application_hybrid(app, env, use_go_frontend)
