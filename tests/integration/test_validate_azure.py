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
    "hello",
    "flask",
    "flask-redis",
    "flask-s3",
    "nginx-flask-mysql",
]

# Examples whose generated names break Azure's per-resource naming rules. These
# are real compiler bugs, not test scaffolding: Azure constrains each resource
# type differently and the compiler applies ad-hoc fixups per call site.
#
#   flask              azurerm_container_registry allows alphanumerics only,
#                      but get_name() keeps the dashes.
#   flask-s3           azurerm_storage_account allows lowercase alphanumerics
#                      only, 3-24 chars; dashes survive here too.
#   nginx-flask-mysql  azurerm_key_vault is capped at 24 chars and
#                      "prod-nginx-flask-mysql-kv" is 25.
NAMING_BUGS = {
    "flask": "container registry name keeps dashes (alphanumeric only)",
    "flask-s3": "storage account name keeps dashes (lowercase alphanumeric only)",
    "nginx-flask-mysql": "key vault name exceeds the 24-character limit",
}


@pytest.mark.parametrize("example_name", AZURE_EXAMPLES)
def test_terraform_validate_azure(example_name, terraform_base, mock_azure_prod_env):
    if example_name in NAMING_BUGS:
        pytest.xfail(NAMING_BUGS[example_name])

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
