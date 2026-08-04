import json
import os
import shutil
import subprocess
import tempfile
import time
from pathlib import Path

import pytest
import requests

from composey.models.environment import AwsEnvironment, AzureEnvironment


@pytest.fixture(scope="session")
def composey_go_binary():
    """
    Build the Go binary once per test session.

    This fixture ensures the Go parser/normalizer binary is available
    for tests that use the hybrid compiler.
    """
    repo_root = Path(__file__).parent.parent
    go_binary = repo_root / "composey-go" / "composey-go"

    # Build the binary if it doesn't exist
    if not go_binary.exists():
        composey_go_dir = repo_root / "composey-go"
        if not composey_go_dir.exists():
            pytest.skip("composey-go directory not found, skipping Go-dependent tests")

        result = subprocess.run(
            ["go", "build", "-o", "composey-go", "./cmd/composey"],
            cwd=composey_go_dir,
            capture_output=True,
            text=True,
        )

        if result.returncode != 0:
            pytest.fail(f"Failed to build Go binary: {result.stderr}")

    return go_binary




# AWS Mock Environment
@pytest.fixture(scope="session")
def mock_prod_env():
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-123",
        public_subnets=["subnet-1", "subnet-2"],
        private_subnets=["subnet-3", "subnet-4"],
        ecs_cluster_arn="arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster",
        alb_arn="arn:aws:lb:us-east-1:123456789012:loadbalancer/app/shared-alb/123",
        alb_listener_arn="arn:aws:lb:us-east-1:123456789012:listener/app/shared-alb/123/456",
        alb_security_group_id="sg-alb0123456789",
    )


# LocalStack Configuration
LS_ENDPOINT = "http://localhost:4566"


@pytest.fixture(scope="session")
def mock_localstack_env():
    return AwsEnvironment(
        name="local",
        vpc_id="vpc-123",
        public_subnets=["subnet-1", "subnet-2"],
        private_subnets=["subnet-3", "subnet-4"],
        ecs_cluster_arn="arn:aws:ecs:us-east-1:123456789012:cluster/local-cluster",
        aws_endpoint=LS_ENDPOINT,
        # A disposable environment: nothing here is worth a final snapshot, and
        # teardown must not stop at a non-empty bucket.
        retain_data_on_destroy=False,
    )


# Azure Mock Environment
@pytest.fixture(scope="session")
def mock_azure_prod_env():
    return AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        # Fully-formed resource IDs. The azurerm provider parses these during
        # `terraform validate`, so abbreviated stand-ins fail before it ever
        # reaches the schema checks the Azure validate test exists to make.
        log_analytics_workspace_id=(
            "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod"
            "/providers/Microsoft.OperationalInsights/workspaces/prod-logs"
        ),
        vnet_id=(
            "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod"
            "/providers/Microsoft.Network/virtualNetworks/prod-vnet"
        ),
        infrastructure_subnet_id=(
            "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/prod"
            "/providers/Microsoft.Network/virtualNetworks/prod-vnet/subnets/infrastructure"
        ),
        container_registry_name="prodacr",
    )


@pytest.fixture(scope="session")
def terraform_base():
    """
    Initializes a shared Terraform directory with the AWS provider.
    Each test will copy the .terraform folder from here to stay fast.
    """
    if shutil.which("terraform") is None:
        pytest.skip("terraform CLI not found")

    base_dir = tempfile.mkdtemp()

    # Create a dummy manifest just to init the providers
    dummy_manifest = {
        "terraform": {
            "required_providers": {
                "aws": {"source": "hashicorp/aws", "version": "~> 5.0"},
                "azurerm": {"source": "hashicorp/azurerm", "version": "~> 4.0"},
                "random": {"source": "hashicorp/random", "version": "~> 3.6"},
                "docker": {"source": "kreuzwerker/docker", "version": "~> 3.0"},
            }
        }
    }

    with open(os.path.join(base_dir, "main.tf.json"), "w") as f:
        json.dump(dummy_manifest, f)

    # Optimization: Use a shared plugin cache to avoid downloading AWS provider every time
    root_dir = os.path.dirname(os.path.dirname(__file__))
    cache_dir = os.path.abspath(os.path.join(root_dir, ".terraform_cache"))
    os.makedirs(cache_dir, exist_ok=True)

    env_vars = os.environ.copy()
    env_vars["TF_PLUGIN_CACHE_DIR"] = cache_dir

    # Init once for the session
    result = subprocess.run(
        ["terraform", "init", "-backend=false", "-input=false"],
        cwd=base_dir,
        capture_output=True,
        text=True,
        env=env_vars,
    )

    if result.returncode != 0:
        shutil.rmtree(base_dir)
        pytest.skip(f"Terraform init failed during session setup: {result.stderr}")

    yield base_dir
    shutil.rmtree(base_dir)


@pytest.fixture(scope="session")
def localstack_session():
    """Starts a LocalStack container for the duration of the test session."""
    if shutil.which("docker") is None:
        pytest.skip("docker not found")

    container_name = "composey-localstack"
    # Stop any existing container
    subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)

    # Start LocalStack
    subprocess.run(
        [
            "docker",
            "run",
            "-d",
            "--name",
            container_name,
            "-p",
            "4566:4566",
            "localstack/localstack:3.0.2",
        ],
        check=True,
    )

    # Wait for LocalStack to be ready
    max_retries = 30
    print(f"Waiting for LocalStack at {LS_ENDPOINT}...")
    for i in range(max_retries):
        try:
            res = requests.get(f"{LS_ENDPOINT}/_localstack/health", timeout=2)
            if res.status_code == 200:
                print(f"LocalStack is ready after {i * 2} seconds.")
                break
        except Exception as e:
            if i % 5 == 0:
                print(f"Retry {i}: LocalStack not ready yet ({type(e).__name__}: {e})")
        time.sleep(2)
    else:
        print("LocalStack failed to respond to health check.")
        subprocess.run(["docker", "logs", container_name])
        pytest.fail("LocalStack failed to start in time")

    yield LS_ENDPOINT

    # Clean up
    subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)
