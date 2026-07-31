"""Networking inference for AWS resources.

Handles security groups, network connectivity, and service discovery.
"""

from typing import Callable

from composey.models.aws import (
    AWSResources,
    SecurityGroup,
    SecurityGroupRule,
    ServiceDiscoveryPrivateDnsNamespace,
    ServiceDiscoveryService,
)
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application as SemanticApp
from composey.models.semantic import Service as SemanticService

from ._common import namespace_for, safe_terraform_identifier


def infer_networking(
    resources: AWSResources,
    app: SemanticApp,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> None:
    """Infer security groups and network rules for the application.

    Creates one security group per compose network. Services sharing a network
    can reach each other, and services on disjoint networks cannot, which is
    what Compose already enforces locally.
    """
    networks = sorted({n for service in app.services for n in service.networks})

    for network in networks:
        key = _sg_key(network)
        resources.aws_security_group[key] = SecurityGroup(
            name=get_name(network),
            vpc_id=env.vpc_id,
            description=f"Network {network} of {app.name} in {env.name}",
            tags=tags,
        )

        # Members of a network reach each other on any port
        resources.aws_security_group_rule[f"{key}_internal_rule"] = SecurityGroupRule(
            type="ingress",
            from_port=0,
            to_port=0,
            protocol="-1",
            security_group_id=f"${{aws_security_group.{key}.id}}",
            source_security_group_id=f"${{aws_security_group.{key}.id}}",
            description=f"Allow traffic within network {network}",
        )

        # Allow outbound access (needed for pulling images, writing logs)
        resources.aws_security_group_rule[f"{key}_egress_rule"] = SecurityGroupRule(
            type="egress",
            from_port=0,
            to_port=0,
            protocol="-1",
            security_group_id=f"${{aws_security_group.{key}.id}}",
            cidr_blocks=["0.0.0.0/0"],
            description=f"Allow all outbound from network {network}",
        )


def infer_service_discovery(
    resources: AWSResources,
    app: SemanticApp,
    env: AwsEnvironment,
    get_name: Callable[[str], str],
    tags: dict[str, str] | None,
) -> str:
    """Create service discovery namespace and services.

    Returns the namespace name for use in connection strings.
    """
    namespace = namespace_for(env.name, app.name)
    discoverable = [s for s in app.services if _is_discoverable(s)]

    if discoverable:
        resources.aws_service_discovery_private_dns_namespace["app"] = (
            ServiceDiscoveryPrivateDnsNamespace(
                name=namespace,
                vpc=env.vpc_id,
                description=f"Service discovery for {app.name} in {env.name}",
                tags=tags,
            )
        )

    for service in discoverable:
        resources.aws_service_discovery_service[
            f"{safe_terraform_identifier(service.name)}_discovery"
        ] = ServiceDiscoveryService(
            name=service.name,
            dns_config={
                "namespace_id": "${aws_service_discovery_private_dns_namespace.app.id}",
                "dns_records": [{"ttl": 10, "type": "A"}],
                "routing_policy": "MULTIVALUE",
            },
            tags=tags,
        )

    return namespace


def security_group_ids(service_networks: list[str]) -> list[str]:
    """Get Terraform references to security groups for given networks."""
    return [
        f"${{aws_security_group.{_sg_key(n)}.id}}" for n in sorted(service_networks)
    ]


def _sg_key(network: str) -> str:
    """Generate a Terraform-safe security group key from a network name."""
    return f"{safe_terraform_identifier(network)}_sg"


def _is_discoverable(service: SemanticService) -> bool:
    """Check if a service can be discovered by other services.

    A scheduled task runs and exits rather than being a service, and a container
    with no port publishes nothing to reach.
    """
    return (
        service.capability == "container"
        and service.port is not None
        and service.schedule is None
    )
