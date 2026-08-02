"""Generate Terraform for shared environment infrastructure.

This module creates the "platform" infrastructure (VPC, ALB, ECS Cluster, etc.)
that multiple applications share. It's used by `composey init env` to set up
environments that developers then deploy to with `composey up`.

This replicates the functionality of the bootstrap/ Terraform module.
"""

import json
import re
from typing import Any, Dict, List, Optional


def _tf_name(name: str) -> str:
    """Convert environment name to valid Terraform resource name.

    Terraform resource names must start with a letter or underscore and contain
    only letters, digits, and underscores.
    """
    # Replace hyphens and other invalid chars with underscores
    tf_name = re.sub(r"[^a-zA-Z0-9_]", "_", name)
    # Ensure it starts with a letter or underscore
    if tf_name and tf_name[0].isdigit():
        tf_name = "_" + tf_name
    return tf_name


def _cidrsubnet(base_cidr: str, newbits: int, netnum: int) -> str:
    """
    Calculate a subnet CIDR using Terraform's cidrsubnet logic.

    Simplified implementation that works for common cases.
    """
    import ipaddress

    network = ipaddress.ip_network(base_cidr)
    # Calculate the subnet size
    new_prefix = network.prefixlen + newbits
    # Calculate the network address offset
    subnet_size = 2 ** (32 - new_prefix)
    network_int = int(network.network_address)
    subnet_int = network_int + (netnum * subnet_size)
    return f"{ipaddress.ip_address(subnet_int)}/{new_prefix}"


def generate_aws_environment(
    name: str,
    region: str,
    vpc_cidr: str = "10.0.0.0/16",
    az_count: int = 2,
    create_alb: bool = True,
    certificate_arn: Optional[str] = None,
    aws_endpoint: Optional[str] = None,
    tags: Optional[Dict[str, str]] = None,
    retain_data_on_destroy: bool = True,
) -> str:
    """
    Generate Terraform JSON for a shared AWS environment.

    Replicates the bootstrap/ Terraform module functionality.

    Creates:
    - VPC with public and private subnets across AZs
    - NAT Gateway(s) for outbound internet from private subnets (one per AZ)
    - Application Load Balancer in public subnets (optional)
    - ECS Cluster for Fargate tasks
    - Security groups for ALB and ECS tasks

    Args:
        name: Environment name (e.g., "prod", "staging")
        region: AWS region
        vpc_cidr: CIDR block for the VPC
        az_count: Number of availability zones to spread subnets across
        create_alb: Whether to create a shared ALB and listener
        certificate_arn: ACM certificate ARN for HTTPS listener
        aws_endpoint: Custom endpoint for AWS services (e.g., LocalStack)
        tags: Default tags applied to all resources
        retain_data_on_destroy: Whether destroying the stack preserves data

    Returns:
        Terraform JSON string for the environment
    """
    if tags is None:
        tags = {}

    # Terraform-safe resource name (no hyphens in resource identifiers)
    tf_name = _tf_name(name)

    # Determine listener port/protocol based on certificate
    listener_port = 443 if certificate_arn else 80
    listener_protocol = "HTTPS" if certificate_arn else "HTTP"

    # Calculate subnet CIDRs using Terraform's cidrsubnet logic
    # Split VPC into /20s: first az_count blocks are public, next are private
    # /16 + 4 bits = /20 subnets
    public_cidrs = [_cidrsubnet(vpc_cidr, 4, i) for i in range(az_count)]
    private_cidrs = [_cidrsubnet(vpc_cidr, 4, i + az_count) for i in range(az_count)]

    # Build the Terraform manifest
    terraform: Dict[str, Any] = {
        "required_version": ">= 1.5",
        "required_providers": {
            "aws": {
                "source": "hashicorp/aws",
                "version": "~> 5.0",
            },
            "local": {
                "source": "hashicorp/local",
                "version": "~> 2.4",
            },
        },
    }

    provider: Dict[str, Any] = {
        "aws": {
            "region": region,
        }
    }

    # Add endpoints for LocalStack support if specified
    if aws_endpoint:
        provider["aws"]["endpoints"] = {
            "ec2": aws_endpoint,
            "ecs": aws_endpoint,
            "elbv2": aws_endpoint,
            "iam": aws_endpoint,
            "sts": aws_endpoint,
            "logs": aws_endpoint,
            "s3": aws_endpoint,
            "secretsmanager": aws_endpoint,
        }

    resources: Dict[str, Any] = {}
    data_sources: Dict[str, Any] = {}

    # Data source for availability zones
    data_sources["aws_availability_zones"] = {
        "available": {
            "state": "available",
        }
    }

    # VPC
    resources["aws_vpc"] = {
        tf_name: {
            "cidr_block": vpc_cidr,
            "enable_dns_support": True,
            "enable_dns_hostnames": True,
            "tags": {**tags, "Name": name, "Environment": name},
        }
    }

    # Internet Gateway
    resources["aws_internet_gateway"] = {
        tf_name: {
            "vpc_id": f"${{aws_vpc.{tf_name}.id}}",
            "tags": {**tags, "Name": name, "Environment": name},
        }
    }

    # Public Subnets
    resources["aws_subnet"] = {}
    for i in range(az_count):
        resources["aws_subnet"][f"{tf_name}_public_{i}"] = {
            "vpc_id": f"${{aws_vpc.{tf_name}.id}}",
            "cidr_block": public_cidrs[i],
            "availability_zone": f"${{data.aws_availability_zones.available.names[{i}]}}",
            "map_public_ip_on_launch": True,
            "tags": {
                **tags,
                "Name": f"{name}-public-{i}",
                "Environment": name,
            },
        }

    # Private Subnets
    for i in range(az_count):
        resources["aws_subnet"][f"{tf_name}_private_{i}"] = {
            "vpc_id": f"${{aws_vpc.{tf_name}.id}}",
            "cidr_block": private_cidrs[i],
            "availability_zone": f"${{data.aws_availability_zones.available.names[{i}]}}",
            "map_public_ip_on_launch": False,
            "tags": {
                **tags,
                "Name": f"{name}-private-{i}",
                "Environment": name,
            },
        }

    # Elastic IPs for NAT Gateways (one per AZ)
    resources["aws_eip"] = {}
    for i in range(az_count):
        resources["aws_eip"][f"{tf_name}_nat_{i}"] = {
            "domain": "vpc",
            "depends_on": [f"aws_internet_gateway.{tf_name}"],
            "tags": {**tags, "Name": f"{name}-nat-{i}", "Environment": name},
        }

    # NAT Gateways (one per AZ)
    resources["aws_nat_gateway"] = {}
    for i in range(az_count):
        resources["aws_nat_gateway"][f"{tf_name}_{i}"] = {
            "allocation_id": f"${{aws_eip.{tf_name}_nat_{i}.id}}",
            "subnet_id": f"${{aws_subnet.{tf_name}_public_{i}.id}}",
            "depends_on": [f"aws_internet_gateway.{tf_name}"],
            "tags": {**tags, "Name": f"{name}-{i}", "Environment": name},
        }

    # Public Route Table
    resources["aws_route_table"] = {
        f"{tf_name}_public": {
            "vpc_id": f"${{aws_vpc.{tf_name}.id}}",
            "tags": {**tags, "Name": f"{name}-public", "Environment": name},
        }
    }

    # Public Route (separate resource instead of inline)
    resources["aws_route"] = {
        f"{tf_name}_public": {
            "route_table_id": f"${{aws_route_table.{tf_name}_public.id}}",
            "destination_cidr_block": "0.0.0.0/0",
            "gateway_id": f"${{aws_internet_gateway.{tf_name}.id}}",
        }
    }

    # Public Route Table Associations
    resources["aws_route_table_association"] = {}
    for i in range(az_count):
        resources["aws_route_table_association"][f"{tf_name}_public_{i}"] = {
            "subnet_id": f"${{aws_subnet.{tf_name}_public_{i}.id}}",
            "route_table_id": f"${{aws_route_table.{tf_name}_public.id}}",
        }

    # Private Route Tables (one per AZ)
    for i in range(az_count):
        resources["aws_route_table"][f"{tf_name}_private_{i}"] = {
            "vpc_id": f"${{aws_vpc.{tf_name}.id}}",
            "tags": {**tags, "Name": f"{name}-private-{i}", "Environment": name},
        }

    # Private Routes (separate resource)
    for i in range(az_count):
        resources["aws_route"][f"{tf_name}_private_{i}"] = {
            "route_table_id": f"${{aws_route_table.{tf_name}_private_{i}.id}}",
            "destination_cidr_block": "0.0.0.0/0",
            "nat_gateway_id": f"${{aws_nat_gateway.{tf_name}_{i}.id}}",
        }

    # Private Route Table Associations
    for i in range(az_count):
        resources["aws_route_table_association"][f"{tf_name}_private_{i}"] = {
            "subnet_id": f"${{aws_subnet.{tf_name}_private_{i}.id}}",
            "route_table_id": f"${{aws_route_table.{tf_name}_private_{i}.id}}",
        }

    # ECS Cluster
    resources["aws_ecs_cluster"] = {
        tf_name: {
            "name": name,
            "setting": [
                {
                    "name": "containerInsights",
                    "value": "enabled",
                }
            ],
            "tags": {**tags, "Environment": name},
        }
    }

    # ECS Cluster Capacity Providers (Fargate + Fargate Spot)
    resources["aws_ecs_cluster_capacity_providers"] = {
        tf_name: {
            "cluster_name": f"${{aws_ecs_cluster.{tf_name}.name}}",
            "capacity_providers": ["FARGATE", "FARGATE_SPOT"],
            "default_capacity_provider_strategy": [
                {
                    "capacity_provider": "FARGATE",
                    "weight": 1,
                }
            ],
        }
    }

    # ALB and related resources (conditional)
    if create_alb:
        # Security Group for ALB - using separate rule resources
        resources["aws_security_group"] = {
            f"{tf_name}_alb": {
                "name": f"{name}-alb",
                "description": f"Ingress to the shared ALB for {name}",
                "vpc_id": f"${{aws_vpc.{tf_name}.id}}",
                "tags": {**tags, "Environment": name},
            }
        }

        # Security Group Rules (separate resources)
        resources["aws_security_group_rule"] = {
            f"{tf_name}_alb_ingress": {
                "type": "ingress",
                "from_port": listener_port,
                "to_port": listener_port,
                "protocol": "tcp",
                "cidr_blocks": ["0.0.0.0/0"],
                "security_group_id": f"${{aws_security_group.{tf_name}_alb.id}}",
                "description": "Allow ingress to ALB",
            },
            f"{tf_name}_alb_egress": {
                "type": "egress",
                "from_port": 0,
                "to_port": 0,
                "protocol": "-1",
                "cidr_blocks": ["0.0.0.0/0"],
                "security_group_id": f"${{aws_security_group.{tf_name}_alb.id}}",
                "description": "Allow all outbound",
            },
        }

        # Application Load Balancer
        public_subnet_ids = [
            f"${{aws_subnet.{tf_name}_public_{i}.id}}" for i in range(az_count)
        ]
        resources["aws_lb"] = {
            tf_name: {
                "name": f"{name}-alb",
                "load_balancer_type": "application",
                "subnets": public_subnet_ids,
                "security_groups": [f"${{aws_security_group.{tf_name}_alb.id}}"],
                "tags": {**tags, "Environment": name},
            }
        }

        # ALB Listener
        listener_config: Dict[str, Any] = {
            "load_balancer_arn": f"${{aws_lb.{tf_name}.arn}}",
            "port": listener_port,
            "protocol": listener_protocol,
            "default_action": [
                {
                    "type": "fixed-response",
                    "fixed_response": {
                        "content_type": "text/plain",
                        "message_body": "Not Found",
                        "status_code": "404",
                    },
                }
            ],
            "tags": {**tags, "Environment": name},
        }

        # Add SSL settings if certificate is provided
        if certificate_arn:
            listener_config["ssl_policy"] = "ELBSecurityPolicy-TLS13-1-2-2021-06"
            listener_config["certificate_arn"] = certificate_arn

        resources["aws_lb_listener"] = {
            tf_name: listener_config,
        }

    # local_file for environment.yml output
    environment_config = {
        "target": "aws",
        "name": name,
        "region": region,
        "vpc_id": f"${{aws_vpc.{tf_name}.id}}",
        "public_subnets": [
            f"${{aws_subnet.{tf_name}_public_{i}.id}}" for i in range(az_count)
        ],
        "private_subnets": [
            f"${{aws_subnet.{tf_name}_private_{i}.id}}" for i in range(az_count)
        ],
        "ecs_cluster_arn": f"${{aws_ecs_cluster.{tf_name}.arn}}",
        "retain_data_on_destroy": retain_data_on_destroy,
    }

    # Add ALB outputs if created
    if create_alb:
        environment_config["alb_arn"] = f"${{aws_lb.{tf_name}.arn}}"
        environment_config["alb_listener_arn"] = f"${{aws_lb_listener.{tf_name}.arn}}"
        environment_config["alb_security_group_id"] = (
            f"${{aws_security_group.{tf_name}_alb.id}}"
        )

    # Add tags if provided
    if tags:
        environment_config["tags"] = tags

    # Add aws_endpoint if provided
    if aws_endpoint:
        environment_config["aws_endpoint"] = aws_endpoint

    resources["local_file"] = {
        f"{tf_name}_environment": {
            "filename": "${path.module}/environment.yml",
            "content": json.dumps(environment_config),
            "file_permission": "0644",
        }
    }

    # Outputs
    outputs: Dict[str, Any] = {
        "environment": {
            "description": "Values matching composey's Environment model.",
            "value": environment_config,
        },
    }

    if create_alb:
        outputs["alb_dns_name"] = {
            "description": "DNS name of the shared ALB.",
            "value": f"${{aws_lb.{tf_name}.dns_name}}",
        }

    manifest: Dict[str, Any] = {
        "terraform": terraform,
        "provider": provider,
    }

    if data_sources:
        manifest["data"] = data_sources

    manifest["resource"] = resources
    manifest["output"] = outputs

    return json.dumps(manifest, indent=2)


def generate_environment_yaml(
    name: str,
    region: str,
    vpc_id: str,
    public_subnets: List[str],
    private_subnets: List[str],
    ecs_cluster_arn: str,
    alb_arn: Optional[str] = None,
    alb_listener_arn: Optional[str] = None,
    alb_security_group_id: Optional[str] = None,
    retain_data_on_destroy: bool = True,
    log_retention_days: int = 30,
    tags: Optional[Dict[str, str]] = None,
    aws_endpoint: Optional[str] = None,
) -> str:
    """
    Generate the environment.yml reference file.

    This is used when NOT using the local_file resource output.

    Args:
        name: Environment name
        region: AWS region
        vpc_id: VPC ID
        public_subnets: List of public subnet IDs
        private_subnets: List of private subnet IDs
        ecs_cluster_arn: ECS Cluster ARN
        alb_arn: ALB ARN (optional)
        alb_listener_arn: ALB listener ARN (optional)
        alb_security_group_id: ALB security group ID (optional)
        retain_data_on_destroy: Data retention policy
        log_retention_days: Log retention policy
        tags: Default tags
        aws_endpoint: Custom AWS endpoint

    Returns:
        YAML string for environment configuration
    """
    import yaml

    config: Dict[str, Any] = {
        "#": f"Generated by: composey init env --provider aws --name {name}",
        "# Usage": f"composey up --env {name}/environment.yml",
        "target": "aws",
        "name": name,
        "region": region,
        "vpc_id": vpc_id,
        "public_subnets": public_subnets,
        "private_subnets": private_subnets,
        "ecs_cluster_arn": ecs_cluster_arn,
        "retain_data_on_destroy": retain_data_on_destroy,
        "log_retention_days": log_retention_days,
    }

    if alb_arn:
        config["alb_arn"] = alb_arn
    if alb_listener_arn:
        config["alb_listener_arn"] = alb_listener_arn
    if alb_security_group_id:
        config["alb_security_group_id"] = alb_security_group_id
    if tags:
        config["tags"] = tags
    if aws_endpoint:
        config["aws_endpoint"] = aws_endpoint

    return yaml.dump(config, default_flow_style=False, sort_keys=False)


def generate_azure_environment(
    name: str,
    location: str,
    vnet_cidr: str = "10.0.0.0/16",
    tags: Optional[Dict[str, str]] = None,
    retain_data_on_destroy: bool = True,
) -> str:
    """
    Generate Terraform JSON for a shared Azure environment.

    Creates:
    - Resource Group
    - Log Analytics Workspace
    - Virtual Network with subnet
    - Container Apps Environment

    Args:
        name: Environment name (e.g., "prod", "staging")
        location: Azure region
        vnet_cidr: CIDR block for the VNet
        tags: Default tags applied to all resources
        retain_data_on_destroy: Whether destroying the stack preserves data

    Returns:
        Terraform JSON string for the environment
    """
    if tags is None:
        tags = {}

    # Terraform-safe resource name
    tf_name = _tf_name(name)

    # Build the Terraform manifest
    terraform: Dict[str, Any] = {
        "required_version": ">= 1.5",
        "required_providers": {
            "azurerm": {
                "source": "hashicorp/azurerm",
                "version": "~> 3.0",
            },
            "local": {
                "source": "hashicorp/local",
                "version": "~> 2.4",
            },
        },
    }

    provider: Dict[str, Any] = {
        "azurerm": {
            "features": {},
        }
    }

    data_sources: Dict[str, Any] = {
        "azurerm_client_config": {
            "current": {},
        }
    }

    resources: Dict[str, Any] = {}

    # Register required Azure Resource Providers
    # This ensures namespaces are registered before creating resources
    resources["null_resource"] = {
        f"{tf_name}_register_providers": {
            "provisioner": [
                {
                    "local-exec": {
                        "command": "az provider register --namespace Microsoft.OperationalInsights --wait && az provider register --namespace Microsoft.ContainerInstance --wait && az provider register --namespace Microsoft.App --wait && az provider register --namespace Microsoft.Network --wait",
                        "interpreter": ["/bin/sh", "-c"],
                    }
                }
            ],
        }
    }

    # Resource Group
    resources["azurerm_resource_group"] = {
        tf_name: {
            "name": name,
            "location": location,
            "tags": {**tags, "Environment": name},
        }
    }

    # Log Analytics Workspace
    resources["azurerm_log_analytics_workspace"] = {
        tf_name: {
            "name": f"{name}-logs",
            "location": location,
            "resource_group_name": f"${{azurerm_resource_group.{tf_name}.name}}",
            "sku": "PerGB2018",
            "retention_in_days": 30,
            "tags": {**tags, "Environment": name},
        }
    }

    # Virtual Network
    resources["azurerm_virtual_network"] = {
        tf_name: {
            "name": f"{name}-vnet",
            "location": location,
            "resource_group_name": f"${{azurerm_resource_group.{tf_name}.name}}",
            "address_space": [vnet_cidr],
            "tags": {**tags, "Environment": name},
        }
    }

    # Subnet for Container Apps
    resources["azurerm_subnet"] = {
        f"{tf_name}_infrastructure": {
            "name": "infrastructure",
            "resource_group_name": f"${{azurerm_resource_group.{tf_name}.name}}",
            "virtual_network_name": f"${{azurerm_virtual_network.{tf_name}.name}}",
            "address_prefixes": [_cidrsubnet(vnet_cidr, 5, 0)],  # /21 subnet
        }
    }

    # Container Apps Environment
    resources["azurerm_container_app_environment"] = {
        tf_name: {
            "name": f"{name}-env",
            "location": location,
            "resource_group_name": f"${{azurerm_resource_group.{tf_name}.name}}",
            "log_analytics_workspace_id": f"${{azurerm_log_analytics_workspace.{tf_name}.id}}",
            "infrastructure_subnet_id": f"${{azurerm_subnet.{tf_name}_infrastructure.id}}",
            "tags": {**tags, "Environment": name},
        }
    }

    # local_file for environment.yml output
    environment_config = {
        "target": "azure",
        "name": name,
        "region": location,
        "resource_group_name": f"${{azurerm_resource_group.{tf_name}.name}}",
        "container_apps_environment_name": f"${{azurerm_container_app_environment.{tf_name}.name}}",
        "log_analytics_workspace_id": f"${{azurerm_log_analytics_workspace.{tf_name}.id}}",
        "vnet_id": f"${{azurerm_virtual_network.{tf_name}.id}}",
        "infrastructure_subnet_id": f"${{azurerm_subnet.{tf_name}_infrastructure.id}}",
        "retain_data_on_destroy": retain_data_on_destroy,
    }

    if tags:
        environment_config["tags"] = tags

    resources["local_file"] = {
        f"{tf_name}_environment": {
            "filename": "${path.module}/environment.yml",
            "content": json.dumps(environment_config),
            "file_permission": "0644",
        }
    }

    # Outputs
    outputs: Dict[str, Any] = {
        "environment": {
            "description": "Values matching composey's Environment model.",
            "value": environment_config,
        },
    }

    manifest: Dict[str, Any] = {
        "terraform": terraform,
        "provider": provider,
        "data": data_sources,
        "resource": resources,
        "output": outputs,
    }

    return json.dumps(manifest, indent=2)


def generate_gcp_environment(
    name: str,
    region: str,
    vpc_cidr: str = "10.0.0.0/16",
    tags: Optional[Dict[str, str]] = None,
    retain_data_on_destroy: bool = True,
) -> str:
    """
    Generate Terraform JSON for a shared GCP environment.

    Creates:
    - VPC Network
    - Subnet
    - Cloud Run domain mapping (optional)
    - Service networking connection for Cloud SQL

    Args:
        name: Environment name (e.g., "prod", "staging")
        region: GCP region
        vpc_cidr: CIDR block for the VPC
        tags: Default labels applied to all resources (as map)
        retain_data_on_destroy: Whether destroying the stack preserves data

    Returns:
        Terraform JSON string for the environment
    """
    if tags is None:
        tags = {}

    # Terraform-safe resource name
    tf_name = _tf_name(name)

    # Build the Terraform manifest
    terraform: Dict[str, Any] = {
        "required_version": ">= 1.5",
        "required_providers": {
            "google": {
                "source": "hashicorp/google",
                "version": "~> 5.0",
            },
            "local": {
                "source": "hashicorp/local",
                "version": "~> 2.4",
            },
        },
    }

    provider: Dict[str, Any] = {
        "google": {
            "region": region,
        }
    }

    resources: Dict[str, Any] = {}

    # VPC Network
    resources["google_compute_network"] = {
        tf_name: {
            "name": f"{name}-vpc",
            "auto_create_subnetworks": False,
        }
    }

    # Subnet
    resources["google_compute_subnetwork"] = {
        tf_name: {
            "name": f"{name}-subnet",
            "region": region,
            "network": f"${{google_compute_network.{tf_name}.id}}",
            "ip_cidr_range": vpc_cidr,
            "private_ip_google_access": True,
        }
    }

    # VPC Connector for Cloud Run (Serverless VPC Access)
    resources["google_vpc_access_connector"] = {
        tf_name: {
            "name": f"{name}-connector",
            "region": region,
            "network": f"${{google_compute_network.{tf_name}.id}}",
            "ip_cidr_range": _cidrsubnet(vpc_cidr, 4, 1),  # /20 subnet from /16
            "min_throughput": 200,
            "max_throughput": 400,
        }
    }

    # Service networking connection for Cloud SQL
    resources["google_compute_global_address"] = {
        f"{tf_name}_service_networking": {
            "name": f"{name}-service-networking",
            "purpose": "VPC_PEERING",
            "address_type": "INTERNAL",
            "prefix_length": 16,
            "network": f"${{google_compute_network.{tf_name}.id}}",
        }
    }

    resources["google_service_networking_connection"] = {
        tf_name: {
            "network": f"${{google_compute_network.{tf_name}.id}}",
            "service": "servicenetworking.googleapis.com",
            "reserved_peering_ranges": [
                f"${{google_compute_global_address.{tf_name}_service_networking.name}}"
            ],
        }
    }

    # local_file for environment.yml output
    environment_config = {
        "target": "gcp",
        "name": name,
        "region": region,
        "vpc_id": f"${{google_compute_network.{tf_name}.id}}",
        "subnet_id": f"${{google_compute_subnetwork.{tf_name}.id}}",
        "vpc_connector_name": f"${{google_vpc_access_connector.{tf_name}.name}}",
        "retain_data_on_destroy": retain_data_on_destroy,
    }

    if tags:
        environment_config["labels"] = tags

    resources["local_file"] = {
        f"{tf_name}_environment": {
            "filename": "${path.module}/environment.yml",
            "content": json.dumps(environment_config),
            "file_permission": "0644",
        }
    }

    # Outputs
    outputs: Dict[str, Any] = {
        "environment": {
            "description": "Values matching composey's Environment model.",
            "value": environment_config,
        },
    }

    manifest: Dict[str, Any] = {
        "terraform": terraform,
        "provider": provider,
        "resource": resources,
        "output": outputs,
    }

    return json.dumps(manifest, indent=2)
