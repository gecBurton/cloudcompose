"""Tests for cli_env.py init command."""

import json

from typer.testing import CliRunner

from composey.cli import app

runner = CliRunner()


class TestInitCommand:
    """Tests for the `composey init` command."""

    def test_init_requires_name(self):
        result = runner.invoke(app, ["init"])
        assert result.exit_code != 0
        assert "--name" in result.output or "required" in result.output.lower()

    def test_init_with_name_creates_files(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app, ["init", "--name", "prod", "--output", str(output_dir)]
        )
        assert result.exit_code == 0
        assert (output_dir / "main.tf.json").exists()

    def test_init_default_provider_is_aws(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app, ["init", "--name", "prod", "--output", str(output_dir)]
        )
        assert result.exit_code == 0

    def test_init_unsupported_provider_fails(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app,
            [
                "init",
                "--name",
                "prod",
                "--provider",
                "azure",
                "--output",
                str(output_dir),
            ],
        )
        assert result.exit_code == 1
        assert "not yet supported" in result.output.lower()

    def test_init_with_region(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app,
            [
                "init",
                "--name",
                "prod",
                "--region",
                "us-west-2",
                "--output",
                str(output_dir),
            ],
        )
        assert result.exit_code == 0
        # Check the generated Terraform
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        assert parsed["provider"]["aws"]["region"] == "us-west-2"

    def test_init_default_region(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app, ["init", "--name", "prod", "--output", str(output_dir)]
        )
        assert result.exit_code == 0
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        assert parsed["provider"]["aws"]["region"] == "eu-west-2"

    def test_init_with_vpc_cidr(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app,
            [
                "init",
                "--name",
                "prod",
                "--vpc-cidr",
                "172.16.0.0/16",
                "--output",
                str(output_dir),
            ],
        )
        assert result.exit_code == 0
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        assert parsed["resource"]["aws_vpc"]["prod"]["cidr_block"] == "172.16.0.0/16"

    def test_init_with_az_count(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app,
            ["init", "--name", "prod", "--az-count", "3", "--output", str(output_dir)],
        )
        assert result.exit_code == 0
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        subnets = parsed["resource"]["aws_subnet"]
        # Should have 6 subnets (3 public + 3 private)
        assert len(subnets) == 6

    def test_init_with_no_alb(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app, ["init", "--name", "prod", "--no-alb", "--output", str(output_dir)]
        )
        assert result.exit_code == 0
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        assert "aws_lb" not in parsed["resource"]
        assert "aws_lb_listener" not in parsed["resource"]

    def test_init_with_certificate_arn(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        cert_arn = "arn:aws:acm:us-east-1:123456789012:certificate/12345678-1234-1234-1234-123456789012"
        result = runner.invoke(
            app,
            [
                "init",
                "--name",
                "prod",
                "--certificate-arn",
                cert_arn,
                "--output",
                str(output_dir),
            ],
        )
        assert result.exit_code == 0
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        listener = parsed["resource"]["aws_lb_listener"]["prod"]
        assert listener["port"] == 443
        assert listener["protocol"] == "HTTPS"
        assert listener["certificate_arn"] == cert_arn

    def test_init_with_aws_endpoint(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app,
            [
                "init",
                "--name",
                "prod",
                "--aws-endpoint",
                "http://localhost:4566",
                "--output",
                str(output_dir),
            ],
        )
        assert result.exit_code == 0
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        assert parsed["provider"]["aws"]["endpoints"]["s3"] == "http://localhost:4566"

    def test_init_with_tags(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app,
            [
                "init",
                "--name",
                "prod",
                "--tags",
                '{"Team": "platform", "CostCenter": "12345"}',
                "--output",
                str(output_dir),
            ],
        )
        assert result.exit_code == 0
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        vpc = parsed["resource"]["aws_vpc"]["prod"]
        assert vpc["tags"]["Team"] == "platform"
        assert vpc["tags"]["CostCenter"] == "12345"

    def test_init_with_invalid_tags_fails(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app,
            [
                "init",
                "--name",
                "prod",
                "--tags",
                "not-valid-json",
                "--output",
                str(output_dir),
            ],
        )
        assert result.exit_code == 1
        assert "Invalid JSON" in result.output

    def test_init_with_hyphenated_name(self, tmp_path):
        output_dir = tmp_path / "my-env-infrastructure"
        result = runner.invoke(
            app, ["init", "--name", "my-env", "--output", str(output_dir)]
        )
        assert result.exit_code == 0
        # Check Terraform uses underscores
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        assert "my_env" in parsed["resource"]["aws_vpc"]

    def test_init_displays_next_steps(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app, ["init", "--name", "prod", "--output", str(output_dir)]
        )
        assert result.exit_code == 0
        assert "terraform init" in result.output
        assert "terraform apply" in result.output
        assert "composey up" in result.output

    def test_init_creates_output_directory_if_not_exists(self, tmp_path):
        output_dir = tmp_path / "nested" / "prod-infrastructure"
        result = runner.invoke(
            app, ["init", "--name", "prod", "--output", str(output_dir)]
        )
        assert result.exit_code == 0
        assert output_dir.exists()
        assert (output_dir / "main.tf.json").exists()

    def test_init_outputs_include_environment_config(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app, ["init", "--name", "prod", "--output", str(output_dir)]
        )
        assert result.exit_code == 0
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        env_output = parsed["output"]["environment"]["value"]
        assert env_output["target"] == "aws"
        assert env_output["name"] == "prod"
        assert "vpc_id" in env_output
        assert "ecs_cluster_arn" in env_output

    def test_init_creates_local_file_resource(self, tmp_path):
        output_dir = tmp_path / "prod-infrastructure"
        result = runner.invoke(
            app, ["init", "--name", "prod", "--output", str(output_dir)]
        )
        assert result.exit_code == 0
        tf_content = (output_dir / "main.tf.json").read_text()
        parsed = json.loads(tf_content)
        assert "local_file" in parsed["resource"]
        assert parsed["resource"]["local_file"]["prod_environment"][
            "filename"
        ].endswith("environment.yml")
