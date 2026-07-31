"""AWS resource inference for Composey.

This module is the entry point for the inference stage of the compiler.
It orchestrates the transformation from semantic model to AWS resources
by delegating to specialized modules.
"""

from composey.models.aws import AWSResources
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application as SemanticApp

from ._common import calculate_listener_priorities, namespace_for, path_patterns
from ._compute import infer_compute_resources
from ._connectivity import infer_networking, infer_service_discovery
from ._edge import infer_edge_resources
from ._managed import infer_managed_services
from ._permissions import infer_permissions_and_wiring
from ._scheduling import _eventbridge_expression, infer_scheduled_tasks

# Re-export internal functions for backward compatibility with tests
# Keep old names for test compatibility
_namespace_for = namespace_for
_path_patterns = path_patterns
_priority_band = (
    calculate_listener_priorities  # Actually returns dict, but kept for compatibility
)

__all__ = [
    "infer",
    "_eventbridge_expression",
    "_namespace_for",
    "_path_patterns",
    "_priority_band",
]


def infer(app: SemanticApp, env: AwsEnvironment) -> AWSResources:
    """Infer AWS resources from a semantic application model.

    This is the main entry point for the inference stage. It orchestrates
    the inference of all AWS resources needed to deploy the application.

    Args:
        app: The semantic application model
        env: The target AWS environment configuration

    Returns:
        AWSResources containing all inferred resources
    """
    resources = AWSResources()

    # Helper for resource naming convention: [env]-[app]-[resource]
    def get_name(resource_name: str) -> str:
        return f"{env.name}-{app.name}-{resource_name}"

    # Helper for tags
    tags = env.tags if env.tags else None

    # Whether tearing the stack down preserves what it holds
    discard = not env.retain_data_on_destroy

    # Step 1: Infer networking (security groups for networks)
    infer_networking(resources, app, env, get_name, tags)

    # Step 2: Calculate listener priorities for public services
    priorities = calculate_listener_priorities(app)

    # Step 3: Create service discovery namespace
    namespace = infer_service_discovery(resources, app, env, get_name, tags)

    # Step 4: Infer managed services (RDS, ElastiCache, S3)
    managed_connections = infer_managed_services(
        resources, app, env, get_name, tags, discard
    )

    # Step 5: Infer compute resources (ECS services, tasks, IAM)
    compute_connections = infer_compute_resources(
        resources,
        app,
        env,
        get_name,
        tags,
        discard,
        priorities,
        namespace,
    )

    # Step 6: Infer scheduled tasks (EventBridge)
    infer_scheduled_tasks(resources, app, env, get_name, tags, discard)

    # Step 7: Infer edge resources (CloudFront, WAF)
    infer_edge_resources(resources, app, env, get_name, tags)

    # Step 8: Wire up connections and permissions
    connections = {**managed_connections, **compute_connections}
    infer_permissions_and_wiring(resources, app, env, get_name, connections)

    return resources
