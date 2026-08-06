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


def parse_and_normalize_go(
    compose_file: str, project_name: str, go_binary_path: str = None
) -> SemanticApplication:
    """
    Use Go binary for parsing and normalization.

    This replaces Python's parse() + normalize() with a single call to the Go binary.

    Args:
        compose_file: Path to docker-compose.yml
        project_name: Project name for resource naming
        go_binary_path: Optional path to Go binary. If not provided, uses default location.

    Returns:
        SemanticApplication model
    """
    if go_binary_path:
        go_binary = Path(go_binary_path)
    else:
        go_binary = Path(__file__).parent.parent.parent / "composey-go" / "composey-go"

    if not go_binary.exists():
        raise RuntimeError(
            f"Go binary not found at {go_binary}. "
            f"Run: cd composey-go && go build -o composey-go ./cmd/composey\n"
            f"Or use the composey_go_binary pytest fixture."
        )

    result = subprocess.run(
        [str(go_binary), "normalize", "--project", project_name, compose_file],
        capture_output=True,
        text=True,
        check=True,
    )

    # Parse JSON into dict
    data = json.loads(result.stdout)

    # Convert dict to SemanticApplication Pydantic model
    # This validates the Go output is correct
    return SemanticApplication(**data)


def compile_application_hybrid(app: SemanticApplication, env: BaseEnvironment) -> str:
    """
    Hybrid compilation: Go frontend (parser + normalizer), Python backend (inference + generation).

    Args:
        app: Semantic application model
        env: Environment configuration (AWS/Azure/GCP)
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
) -> str:
    """
    Compile compose file to Terraform using the Go parser/normalizer.

    Args:
        compose_file: Path to docker-compose.yml
        env: Environment configuration
        project_name: Project name for resource naming

    Returns:
        Terraform JSON string
    """
    app = parse_and_normalize_go(compose_file, project_name)
    return compile_application_hybrid(app, env)
