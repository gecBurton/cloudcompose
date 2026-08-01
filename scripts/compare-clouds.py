#!/usr/bin/env python3
"""Cross-Cloud Comparison Script

Generates Terraform for the same Docker Compose file across all three clouds
and analyzes differences to identify abstraction gaps.
"""

import json
import sys
from typing import Dict, Any

sys.path.insert(0, "/Users/GBurton/PycharmProjects/composey")

from composey.compiler import compile_to_terraform
from composey.models.environment import AwsEnvironment, AzureEnvironment, GcpEnvironment


# Test compose file - representative full-stack app
COMPOSE_FILE = "/Users/GBurton/PycharmProjects/composey/examples/flask/compose.yml"
PROJECT_NAME = "flask-demo"

# Environment configurations
AWS_ENV = AwsEnvironment(
    name="prod",
    vpc_id="vpc-12345678",
    public_subnets=["subnet-public-1", "subnet-public-2"],
    private_subnets=["subnet-private-1", "subnet-private-2"],
    ecs_cluster_arn="arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster",
    alb_arn="arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/shared/123",
    alb_listener_arn="arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/shared/123/456",
    alb_security_group_id="sg-alb0123456789",
)

AZURE_ENV = AzureEnvironment(
    name="prod",
    region="eastus",
    container_apps_environment_name="prod-env",
    log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
    vnet_id="/subscriptions/123/vnets/prod",
    infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    container_registry_name="prodacr",
)

GCP_ENV = GcpEnvironment(
    name="prod",
    project_id="my-gcp-project",
    region="us-central1",
)


def count_resources(tf_json: str) -> Dict[str, int]:
    """Count resources by type in Terraform output."""
    data = json.loads(tf_json)
    resources = data.get("resource", {})
    return {k: len(v) for k, v in resources.items()}


def extract_key_config(tf_json: str, path: list) -> Any:
    """Extract specific configuration from Terraform JSON."""
    data = json.loads(tf_json)
    try:
        for key in path:
            data = data[key]
        return data
    except KeyError, TypeError:
        return None


def compare_across_clouds():
    """Generate and compare Terraform for all three clouds."""
    print("=" * 80)
    print("CROSS-CLOUD COMPARISON: Flask App")
    print("=" * 80)
    print()

    # Generate for each cloud
    results = {}
    for name, env in [("AWS", AWS_ENV), ("Azure", AZURE_ENV), ("GCP", GCP_ENV)]:
        print(f"Generating for {name}...")
        try:
            tf_json = compile_to_terraform(COMPOSE_FILE, env, PROJECT_NAME)
            results[name] = {
                "json": tf_json,
                "resources": count_resources(tf_json),
                "size": len(tf_json),
            }
            print(f"  ✓ Generated {len(tf_json):,} characters")
        except Exception as e:
            print(f"  ✗ Error: {e}")
            results[name] = {"error": str(e)}
        print()

    # Resource count comparison
    print("-" * 80)
    print("RESOURCE COUNT COMPARISON")
    print("-" * 80)
    print()

    all_resource_types = set()
    for r in results.values():
        if "resources" in r:
            all_resource_types.update(r["resources"].keys())

    # Group by category
    categories = {
        "Compute": [
            "aws_ecs_service",
            "aws_ecs_task_definition",
            "azurerm_container_app",
            "google_cloud_run_service",
        ],
        "Database": [
            "aws_db_instance",
            "azurerm_postgresql_flexible_server",
            "google_sql_database_instance",
        ],
        "Networking": [
            "aws_security_group",
            "aws_lb",
            "azurerm_container_app_environment",
            "google_vpc_access_connector",
        ],
        "IAM": ["aws_iam_role", "aws_iam_role_policy"],
        "Storage": [
            "aws_s3_bucket",
            "azurerm_storage_account",
            "google_storage_bucket",
        ],
    }

    for category, resource_patterns in categories.items():
        print(f"{category}:")
        for cloud_name, result in results.items():
            if "resources" not in result:
                continue
            count = sum(
                result["resources"].get(r, 0)
                for r in result["resources"]
                if any(pattern in r for pattern in resource_patterns)
            )
            print(f"  {cloud_name:6}: {count:2} resources")
        print()

    # Detailed comparison
    print("-" * 80)
    print("DETAILED FINDINGS")
    print("-" * 80)
    print()

    findings = []

    # Finding 1: Compute Platform Complexity
    print("1. COMPUTE PLATFORM COMPLEXITY")
    print()
    aws_compute = results["AWS"]["resources"].get("aws_ecs_service", 0) + results[
        "AWS"
    ]["resources"].get("aws_ecs_task_definition", 0)
    azure_compute = results["Azure"]["resources"].get("azurerm_container_app", 0)
    gcp_compute = results["GCP"]["resources"].get("google_cloud_run_service", 0)

    print(f"   AWS:   {aws_compute} resources (ECS Service + Task Definition)")
    print(f"   Azure: {azure_compute} resource (Container App)")
    print(f"   GCP:   {gcp_compute} resource (Cloud Run Service)")
    print()
    finding = (
        "AWS requires more resources for compute (separate service + task definition)"
    )
    findings.append(("COMPLEXITY", finding))
    print(f"   → {finding}")
    print()

    # Finding 2: Load Balancer
    print("2. LOAD BALANCER REQUIREMENT")
    print()
    aws_lb = results["AWS"]["resources"].get("aws_lb", 0) + results["AWS"][
        "resources"
    ].get("aws_lb_target_group", 0)
    azure_lb = "Built-in (Container Apps)"
    gcp_lb = "Built-in (Cloud Run)"

    print(f"   AWS:   {aws_lb} resources (ALB + Target Group)")
    print(f"   Azure: {azure_lb}")
    print(f"   GCP:   {gcp_lb}")
    print()
    finding = "AWS requires explicit load balancer; Azure/GCP have built-in HTTPS"
    findings.append(("ARCHITECTURE", finding))
    print(f"   → {finding}")
    print()

    # Finding 3: Database Configuration
    print("3. DATABASE CONFIGURATION")
    print()
    aws_db = extract_key_config(
        results["AWS"]["json"],
        ["resource", "aws_db_instance", "db_db", "instance_class"],
    )
    azure_db = extract_key_config(
        results["Azure"]["json"],
        ["resource", "azurerm_postgresql_flexible_server", "main", "sku_name"],
    )
    gcp_db = extract_key_config(
        results["GCP"]["json"],
        ["resource", "google_sql_database_instance", "main", "tier"],
    )

    print(f"   AWS:   {aws_db or 'N/A'}")
    print(f"   Azure: {azure_db or 'N/A'}")
    print(f"   GCP:   {gcp_db or 'N/A'}")
    print()
    finding = (
        "Different sizing models: AWS (instance class), Azure (sku_name), GCP (tier)"
    )
    findings.append(("ABSTRACTION", finding))
    print(f"   → {finding}")
    print()

    # Finding 4: Networking
    print("4. NETWORKING COMPLEXITY")
    print()
    aws_network = results["AWS"]["resources"].get("aws_security_group", 0) + results[
        "AWS"
    ]["resources"].get("aws_db_subnet_group", 0)
    azure_network = results["Azure"]["resources"].get(
        "azurerm_container_app_environment", 0
    )
    gcp_network = results["GCP"]["resources"].get("google_vpc_access_connector", 0)

    print(f"   AWS:   {aws_network} resources (Security Groups + Subnet Groups)")
    print(f"   Azure: {azure_network} resource (Container Apps Environment with VNet)")
    print(f"   GCP:   {gcp_network} resource (VPC Connector for private access)")
    print()
    finding = "AWS has most networking resources; GCP requires VPC connector for private DB access"
    findings.append(("ARCHITECTURE", finding))
    print(f"   → {finding}")
    print()

    # Finding 5: Total Resource Count
    print("5. TOTAL RESOURCE OVERHEAD")
    print()
    for cloud_name, result in results.items():
        if "resources" in result:
            total = sum(result["resources"].values())
            print(f"   {cloud_name:6}: {total:2} total resources")
    print()

    aws_total = sum(results["AWS"]["resources"].values())
    azure_total = sum(results["Azure"]["resources"].values())
    gcp_total = sum(results["GCP"]["resources"].values())

    finding = (
        f"AWS has most overhead ({aws_total} resources), GCP has least ({gcp_total})"
    )
    findings.append(("COMPLEXITY", finding))
    print(f"   → {finding}")
    print()

    # Summary
    print("=" * 80)
    print("SUMMARY OF ABSTRACTION GAPS")
    print("=" * 80)
    print()

    by_category = {}
    for category, finding in findings:
        by_category.setdefault(category, []).append(finding)

    for category, items in by_category.items():
        print(f"{category}:")
        for item in items:
            print(f"  • {item}")
        print()

    print("=" * 80)
    print("RECOMMENDATIONS")
    print("=" * 80)
    print()
    print("1. COMPUTE:")
    print("   → Abstract 'service' concept - hide ECS service/task split")
    print("   → Cloud Run is closest to ideal (single resource, built-in HTTPS)")
    print()
    print("2. LOAD BALANCER:")
    print("   → Don't expose LB to users for simple cases")
    print("   → Use built-in URLs where possible (Azure/GCP style)")
    print()
    print("3. DATABASE SIZING:")
    print("   → Keep our 'small/medium/large' abstraction")
    print("   → Map to cloud-specific SKUs internally")
    print()
    print("4. NETWORKING:")
    print("   → Hide VPC/security group complexity")
    print("   → Auto-create VPC connector on GCP when needed")
    print()
    print("5. GENERAL:")
    print("   → GCP is simplest - use as design target")
    print("   → Backport simplicity to AWS/Azure where possible")
    print()


if __name__ == "__main__":
    compare_across_clouds()
