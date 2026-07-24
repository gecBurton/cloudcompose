from ..models.environment import AwsEnvironment, BaseEnvironment
from .generator import generate
from .inference import infer
from .normalizer import normalize
from .parser import parse


def compile_to_terraform(
    compose_file: str, env: BaseEnvironment, project_name: str
) -> str:
    # 1. Parse
    docker_app = parse(compose_file)

    # 2. Normalize
    semantic_app = normalize(docker_app, project_name)

    # Parsing and normalization are target-agnostic; inference and generation
    # are not, so the backend is selected from the environment's target.
    if not isinstance(env, AwsEnvironment):
        raise NotImplementedError(
            f"No compiler backend is implemented for target {env.target!r}"
        )

    # 3. Infer
    aws_resources = infer(semantic_app, env)

    # 4. Generate
    return generate(aws_resources, env)
