"""Azure golden file tests.

These tests verify that Azure compilation produces expected Terraform output.
They work like the AWS golden tests but for Azure infrastructure.
"""

import json
import os

import pytest
from utils import get_examples

from composey.compiler import compile_to_terraform

# Azure-compatible examples (subset that works on Azure)
AZURE_EXAMPLES = [
    # Deployable on Azure (offered by the acceptance workflow).
    "hello",
    "web-api",
    "minio-s3",
    "production-stack",
    # Compile-only: these need a container build, or name a local-only image,
    # so they are golden/validate coverage rather than smoke-test candidates.
    "build-webapp",
    "doctor",
    "flask",
    "flask-redis",
    "flask-s3",
    "nginx-flask-mysql",
]


def get_azure_examples():
    """Get examples that are compatible with Azure."""
    all_examples = get_examples()
    return [e for e in AZURE_EXAMPLES if e in all_examples]


@pytest.mark.parametrize("example_name", get_azure_examples())
def test_azure_golden_examples(example_name, mock_azure_prod_env):
    """Verify Azure output matches golden files."""
    root_dir = os.path.dirname(os.path.dirname(os.path.dirname(__file__)))
    example_path = os.path.join(root_dir, "examples", example_name)
    compose_path = os.path.join(example_path, "compose.yml")

    # Azure expected files go in expected/azure/
    expected_dir = os.path.join(example_path, "expected", "azure")
    expected_path = os.path.join(expected_dir, "main.tf.json")

    # Skip if compose file doesn't exist
    if not os.path.exists(compose_path):
        pytest.skip(f"Compose file not found: {compose_path}")

    # Run the compiler with Azure environment
    actual_tf_json = compile_to_terraform(
        compose_path, mock_azure_prod_env, example_name
    )

    # Update logic: If the expected file doesn't exist, create it
    if not os.path.exists(expected_path):
        os.makedirs(expected_dir, exist_ok=True)
        with open(expected_path, "w") as f:
            f.write(actual_tf_json)
        pytest.skip(
            f"Generated Azure expected file for example: {example_name}. Review and commit it."
        )

    with open(expected_path, "r") as f:
        expected_tf_json = f.read()

    assert json.loads(actual_tf_json) == json.loads(expected_tf_json)
