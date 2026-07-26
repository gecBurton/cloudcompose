from ..models.environment import AwsEnvironment, BaseEnvironment
from ..models.semantic import Application as SemanticApplication
from .generator import generate
from .inference import infer
from .normalizer import normalize
from .parser import parse


def compile_application(app: SemanticApplication, env: BaseEnvironment) -> str:
    """
    Compile a normalized application for the environment's target.

    Parsing and normalization are target-agnostic; inference and generation are
    not, so the backend is selected here.
    """
    if not isinstance(env, AwsEnvironment):
        raise NotImplementedError(
            f"No compiler backend is implemented for target {env.target!r}"
        )

    return generate(infer(app, env), env)


def compile_to_terraform(
    compose_file: str, env: BaseEnvironment, project_name: str
) -> str:
    return compile_application(normalize(parse(compose_file), project_name), env)
