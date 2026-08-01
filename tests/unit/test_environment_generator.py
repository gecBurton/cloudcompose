"""Tests for environment_generator.py"""

import json

import yaml

from composey.environment_generator import (
    _tf_name,
    generate_aws_environment,
    generate_environment_yaml,
)


class TestTfName:
    """Tests for the _tf_name helper function."""

    def test_simple_name_unchanged(self):
        assert _tf_name("prod") == "prod"
        assert _tf_name("staging") == "staging"

    def test_hyphens_converted_to_underscores(self):
        assert _tf_name("test-env") == "test_env"
        assert _tf_name("my-app-prod") == "my_app_prod"

    def test_leading_digit_gets_underscore_prefix(self):
        assert _tf_name("123env") == "_123env"

    def test_special_chars_converted_to_underscores(self):
        assert _tf_name("env@prod") == "env_prod"
        assert _tf_name("env.prod") == "env_prod"
        assert _tf_name("env/prod") == "env_prod"


class TestGenerateAwsEnvironment:
    """Tests for generate_aws_environment function."""

    def test_generates_valid_json(self):
        result = generate_aws_environment("prod", "eu-west-2")
        parsed = json.loads(result)
        assert "terraform" in parsed
        assert "provider" in parsed
        assert "resource" in parsed
        assert "output" in parsed

    def test_creates_vpc(self):
        result = generate_aws_environment("prod", "eu-west-2")
        parsed = json.loads(result)
        assert "aws_vpc" in parsed["resource"]
        assert "prod" in parsed["resource"]["aws_vpc"]
        assert parsed["resource"]["aws_vpc"]["prod"]["cidr_block"] == "10.0.0.0/16"

    def test_creates_subnets(self):
        result = generate_aws_environment("prod", "eu-west-2", az_count=2)
        parsed = json.loads(result)
        subnets = parsed["resource"]["aws_subnet"]
        # Should have 4 subnets (2 public + 2 private)
        assert "prod_public_0" in subnets
        assert "prod_public_1" in subnets
        assert "prod_private_0" in subnets
        assert "prod_private_1" in subnets

    def test_creates_alb_by_default(self):
        result = generate_aws_environment("prod", "eu-west-2")
        parsed = json.loads(result)
        assert "aws_lb" in parsed["resource"]
        assert "prod" in parsed["resource"]["aws_lb"]
        assert "aws_lb_listener" in parsed["resource"]
        assert "prod" in parsed["resource"]["aws_lb_listener"]

    def test_can_skip_alb_creation(self):
        result = generate_aws_environment("prod", "eu-west-2", create_alb=False)
        parsed = json.loads(result)
        assert "aws_lb" not in parsed["resource"]
        assert "aws_lb_listener" not in parsed["resource"]

    def test_creates_ecs_cluster(self):
        result = generate_aws_environment("prod", "eu-west-2")
        parsed = json.loads(result)
        assert "aws_ecs_cluster" in parsed["resource"]
        assert "prod" in parsed["resource"]["aws_ecs_cluster"]

    def test_creates_ecs_capacity_providers(self):
        result = generate_aws_environment("prod", "eu-west-2")
        parsed = json.loads(result)
        assert "aws_ecs_cluster_capacity_providers" in parsed["resource"]

    def test_creates_security_groups_when_alb_enabled(self):
        result = generate_aws_environment("prod", "eu-west-2", create_alb=True)
        parsed = json.loads(result)
        assert "aws_security_group" in parsed["resource"]
        assert "prod_alb" in parsed["resource"]["aws_security_group"]

    def test_creates_nat_gateways(self):
        result = generate_aws_environment("prod", "eu-west-2", az_count=2)
        parsed = json.loads(result)
        assert "aws_nat_gateway" in parsed["resource"]
        assert "aws_eip" in parsed["resource"]
        # Should have 2 NAT gateways (one per AZ)
        assert "prod_0" in parsed["resource"]["aws_nat_gateway"]
        assert "prod_1" in parsed["resource"]["aws_nat_gateway"]

    def test_custom_az_count(self):
        result = generate_aws_environment("prod", "eu-west-2", az_count=3)
        parsed = json.loads(result)
        # Should have 6 subnets (3 public + 3 private)
        subnets = parsed["resource"]["aws_subnet"]
        assert len(subnets) == 6

    def test_custom_vpc_cidr(self):
        result = generate_aws_environment("prod", "eu-west-2", vpc_cidr="172.16.0.0/16")
        parsed = json.loads(result)
        assert parsed["resource"]["aws_vpc"]["prod"]["cidr_block"] == "172.16.0.0/16"

    def test_hyphenated_name_converted_to_underscores(self):
        result = generate_aws_environment("my-env", "eu-west-2")
        parsed = json.loads(result)
        # Resource names should use underscores
        assert "my_env" in parsed["resource"]["aws_vpc"]
        assert "my_env_alb" in parsed["resource"]["aws_security_group"]
        # But AWS resource names (Name tags) should keep the hyphen
        assert parsed["resource"]["aws_vpc"]["my_env"]["tags"]["Name"] == "my-env"

    def test_outputs_include_required_fields(self):
        result = generate_aws_environment("prod", "eu-west-2")
        parsed = json.loads(result)
        outputs = parsed["output"]
        assert "environment" in outputs
        env_output = outputs["environment"]["value"]
        required_fields = [
            "target",
            "name",
            "region",
            "vpc_id",
            "public_subnets",
            "private_subnets",
            "ecs_cluster_arn",
            "retain_data_on_destroy",
        ]
        for field in required_fields:
            assert field in env_output, f"Missing field: {field}"

    def test_outputs_include_alb_when_created(self):
        result = generate_aws_environment("prod", "eu-west-2", create_alb=True)
        parsed = json.loads(result)
        env_output = parsed["output"]["environment"]["value"]
        assert "alb_arn" in env_output
        assert "alb_listener_arn" in env_output
        assert "alb_security_group_id" in env_output

    def test_outputs_exclude_alb_when_not_created(self):
        result = generate_aws_environment("prod", "eu-west-2", create_alb=False)
        parsed = json.loads(result)
        env_output = parsed["output"]["environment"]["value"]
        assert "alb_arn" not in env_output
        assert "alb_listener_arn" not in env_output

    def test_alb_dns_name_output(self):
        result = generate_aws_environment("prod", "eu-west-2", create_alb=True)
        parsed = json.loads(result)
        assert "alb_dns_name" in parsed["output"]

    def test_https_listener_when_certificate_provided(self):
        result = generate_aws_environment(
            "prod",
            "eu-west-2",
            certificate_arn="arn:aws:acm:us-east-1:123:certificate/abc",
        )
        parsed = json.loads(result)
        listener = parsed["resource"]["aws_lb_listener"]["prod"]
        assert listener["port"] == 443
        assert listener["protocol"] == "HTTPS"
        assert "certificate_arn" in listener
        assert "ssl_policy" in listener

    def test_http_listener_when_no_certificate(self):
        result = generate_aws_environment("prod", "eu-west-2", certificate_arn=None)
        parsed = json.loads(result)
        listener = parsed["resource"]["aws_lb_listener"]["prod"]
        assert listener["port"] == 80
        assert listener["protocol"] == "HTTP"

    def test_container_insights_enabled(self):
        result = generate_aws_environment("prod", "eu-west-2")
        parsed = json.loads(result)
        cluster = parsed["resource"]["aws_ecs_cluster"]["prod"]
        settings = {s["name"]: s["value"] for s in cluster["setting"]}
        assert settings["containerInsights"] == "enabled"

    def test_fargate_capacity_provider(self):
        result = generate_aws_environment("prod", "eu-west-2")
        parsed = json.loads(result)
        cap_providers = parsed["resource"]["aws_ecs_cluster_capacity_providers"]["prod"]
        assert "FARGATE" in cap_providers["capacity_providers"]
        assert "FARGATE_SPOT" in cap_providers["capacity_providers"]

    def test_tags_applied_to_resources(self):
        result = generate_aws_environment(
            "prod", "eu-west-2", tags={"Team": "platform", "CostCenter": "12345"}
        )
        parsed = json.loads(result)
        vpc = parsed["resource"]["aws_vpc"]["prod"]
        assert vpc["tags"]["Team"] == "platform"
        assert vpc["tags"]["CostCenter"] == "12345"
        assert vpc["tags"]["Environment"] == "prod"

    def test_retain_data_flag_in_output(self):
        result = generate_aws_environment(
            "prod", "eu-west-2", retain_data_on_destroy=False
        )
        parsed = json.loads(result)
        env_output = parsed["output"]["environment"]["value"]
        assert env_output["retain_data_on_destroy"] is False

    def test_local_file_resource_creates_environment_yml(self):
        result = generate_aws_environment("prod", "eu-west-2")
        parsed = json.loads(result)
        assert "local_file" in parsed["resource"]
        local_file = parsed["resource"]["local_file"]["prod_environment"]
        assert local_file["filename"] == "${path.module}/environment.yml"


class TestGenerateEnvironmentYaml:
    """Tests for generate_environment_yaml function."""

    def test_generates_valid_yaml(self):
        result = generate_environment_yaml(
            name="prod",
            region="eu-west-2",
            vpc_id="vpc-123",
            public_subnets=["subnet-1", "subnet-2"],
            private_subnets=["subnet-3", "subnet-4"],
            ecs_cluster_arn="arn:aws:ecs:eu-west-2:123:cluster/prod",
        )
        parsed = yaml.safe_load(result)
        assert parsed["target"] == "aws"
        assert parsed["name"] == "prod"
        assert parsed["region"] == "eu-west-2"

    def test_includes_all_required_fields(self):
        result = generate_environment_yaml(
            name="prod",
            region="eu-west-2",
            vpc_id="vpc-123",
            public_subnets=["subnet-1", "subnet-2"],
            private_subnets=["subnet-3", "subnet-4"],
            ecs_cluster_arn="arn:aws:ecs:eu-west-2:123:cluster/prod",
        )
        parsed = yaml.safe_load(result)
        required_fields = [
            "target",
            "name",
            "region",
            "vpc_id",
            "public_subnets",
            "private_subnets",
            "ecs_cluster_arn",
            "retain_data_on_destroy",
            "log_retention_days",
        ]
        for field in required_fields:
            assert field in parsed, f"Missing field: {field}"

    def test_includes_alb_fields_when_provided(self):
        result = generate_environment_yaml(
            name="prod",
            region="eu-west-2",
            vpc_id="vpc-123",
            public_subnets=["subnet-1", "subnet-2"],
            private_subnets=["subnet-3", "subnet-4"],
            ecs_cluster_arn="arn:aws:ecs:eu-west-2:123:cluster/prod",
            alb_arn="arn:aws:elasticloadbalancing:eu-west-2:123:loadbalancer/app/prod",
            alb_listener_arn="arn:aws:elasticloadbalancing:eu-west-2:123:listener/app/prod",
            alb_security_group_id="sg-123",
        )
        parsed = yaml.safe_load(result)
        assert (
            parsed["alb_arn"]
            == "arn:aws:elasticloadbalancing:eu-west-2:123:loadbalancer/app/prod"
        )
        assert (
            parsed["alb_listener_arn"]
            == "arn:aws:elasticloadbalancing:eu-west-2:123:listener/app/prod"
        )
        assert parsed["alb_security_group_id"] == "sg-123"

    def test_default_log_retention(self):
        result = generate_environment_yaml(
            name="prod",
            region="eu-west-2",
            vpc_id="vpc-123",
            public_subnets=["subnet-1"],
            private_subnets=["subnet-2"],
            ecs_cluster_arn="arn:aws:ecs:eu-west-2:123:cluster/prod",
        )
        parsed = yaml.safe_load(result)
        assert parsed["log_retention_days"] == 30

    def test_default_retain_data(self):
        result = generate_environment_yaml(
            name="prod",
            region="eu-west-2",
            vpc_id="vpc-123",
            public_subnets=["subnet-1"],
            private_subnets=["subnet-2"],
            ecs_cluster_arn="arn:aws:ecs:eu-west-2:123:cluster/prod",
        )
        parsed = yaml.safe_load(result)
        assert parsed["retain_data_on_destroy"] is True

    def test_custom_log_retention(self):
        result = generate_environment_yaml(
            name="prod",
            region="eu-west-2",
            vpc_id="vpc-123",
            public_subnets=["subnet-1"],
            private_subnets=["subnet-2"],
            ecs_cluster_arn="arn:aws:ecs:eu-west-2:123:cluster/prod",
            log_retention_days=7,
        )
        parsed = yaml.safe_load(result)
        assert parsed["log_retention_days"] == 7

    def test_includes_comments(self):
        result = generate_environment_yaml(
            name="prod",
            region="eu-west-2",
            vpc_id="vpc-123",
            public_subnets=["subnet-1"],
            private_subnets=["subnet-2"],
            ecs_cluster_arn="arn:aws:ecs:eu-west-2:123:cluster/prod",
        )
        assert "Generated by: composey init env" in result
        assert "Usage" in result

    def test_custom_tags(self):
        result = generate_environment_yaml(
            name="prod",
            region="eu-west-2",
            vpc_id="vpc-123",
            public_subnets=["subnet-1"],
            private_subnets=["subnet-2"],
            ecs_cluster_arn="arn:aws:ecs:eu-west-2:123:cluster/prod",
            tags={"Team": "platform"},
        )
        parsed = yaml.safe_load(result)
        assert parsed["tags"]["Team"] == "platform"

    def test_aws_endpoint(self):
        result = generate_environment_yaml(
            name="prod",
            region="eu-west-2",
            vpc_id="vpc-123",
            public_subnets=["subnet-1"],
            private_subnets=["subnet-2"],
            ecs_cluster_arn="arn:aws:ecs:eu-west-2:123:cluster/prod",
            aws_endpoint="http://localhost:4566",
        )
        parsed = yaml.safe_load(result)
        assert parsed["aws_endpoint"] == "http://localhost:4566"
