"""
Azure counterpart to test_validate.py.

The golden tests only compare Azure output against checked-in fixtures, so a
fixture that encodes an invalid manifest makes them agree with each other and
with nothing else. Terraform is the authority on the provider schema; without
this test, schema errors surface only after a live Azure apply, roughly twenty
minutes into an acceptance run.
"""

import os
import shutil
import subprocess
import tempfile

import pytest

from composey.compiler import compile_to_terraform

# Mirrors AZURE_EXAMPLES in test_azure_golden.py.
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


@pytest.mark.parametrize("example_name", AZURE_EXAMPLES)
def test_terraform_validate_azure(example_name, terraform_base, mock_azure_prod_env):
    root_dir = os.path.dirname(os.path.dirname(os.path.dirname(__file__)))
    compose_path = os.path.join(root_dir, "examples", example_name, "compose.yml")

    tf_json = compile_to_terraform(compose_path, mock_azure_prod_env, example_name)

    with tempfile.TemporaryDirectory() as tmpdir:
        # Copy the pre-initialized .terraform folder (instantly skip init)
        shutil.copytree(
            os.path.join(terraform_base, ".terraform"),
            os.path.join(tmpdir, ".terraform"),
        )
        shutil.copy(
            os.path.join(terraform_base, ".terraform.lock.hcl"),
            os.path.join(tmpdir, ".terraform.lock.hcl"),
        )

        with open(os.path.join(tmpdir, "main.tf.json"), "w") as f:
            f.write(tf_json)

        result = subprocess.run(
            ["terraform", "validate", "-json", "-no-color"],
            cwd=tmpdir,
            capture_output=True,
            text=True,
        )

        if result.returncode != 0:
            pytest.fail(
                f"Terraform validation failed for {example_name}: {result.stdout}"
            )
