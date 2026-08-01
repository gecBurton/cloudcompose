from ..models.environment import (
    AwsEnvironment,
    AzureEnvironment,
    BaseEnvironment,
    GcpEnvironment,
)
from ..models.semantic import Application as SemanticApplication
from .generator import generate as generate_aws
from .generator_azure import generate as generate_azure
from .generator_gcp import generate as generate_gcp
from .inference import infer as infer_aws
from .inference.azure import infer as infer_azure
from .inference.gcp import infer as infer_gcp
from .normalizer import normalize
from .parser import parse


def compile_application(app: SemanticApplication, env: BaseEnvironment) -> str:
    """
    Compile a normalized application for the environment's target.

    Parsing and normalization are target-agnostic; inference and generation are
    not, so the backend is selected here.
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


def compile_to_terraform(
    compose_file: str, env: BaseEnvironment, project_name: str
) -> str:
    return compile_application(normalize(parse(compose_file), project_name), env)
