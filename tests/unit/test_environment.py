import pytest
import yaml

from composey.models.environment import AwsEnvironment, load_environment

AWS_SETTINGS = {
    "name": "prod",
    "vpc_id": "vpc-123",
    "public_subnets": ["subnet-1"],
    "private_subnets": ["subnet-2"],
    "ecs_cluster_arn": "arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster",
}


def _write(tmp_path, settings) -> str:
    path = tmp_path / "environment.yml"
    path.write_text(yaml.safe_dump(settings))
    return str(path)


def test_loads_explicit_aws_target(tmp_path):
    env = load_environment(_write(tmp_path, {"target": "aws", **AWS_SETTINGS}))

    assert isinstance(env, AwsEnvironment)
    assert env.target == "aws"
    assert env.vpc_id == "vpc-123"


def test_target_defaults_to_aws(tmp_path):
    # Environment files written before composey supported a target field must
    # keep working.
    env = load_environment(_write(tmp_path, AWS_SETTINGS))

    assert isinstance(env, AwsEnvironment)
    assert env.target == "aws"


def test_unsupported_target_names_the_supported_ones(tmp_path):
    path = _write(tmp_path, {"target": "unknown-provider", **AWS_SETTINGS})

    with pytest.raises(ValueError, match="unsupported target 'unknown-provider'"):
        load_environment(path)


def test_unknown_field_is_rejected(tmp_path):
    path = _write(tmp_path, {**AWS_SETTINGS, "vpc": "vpc-123"})

    with pytest.raises(Exception, match="vpc"):
        load_environment(path)


def test_missing_required_field_is_rejected(tmp_path):
    settings = {k: v for k, v in AWS_SETTINGS.items() if k != "ecs_cluster_arn"}

    with pytest.raises(Exception, match="ecs_cluster_arn"):
        load_environment(_write(tmp_path, settings))


def test_non_mapping_file_is_rejected(tmp_path):
    path = tmp_path / "environment.yml"
    path.write_text("- not a mapping\n")

    with pytest.raises(ValueError, match="mapping"):
        load_environment(str(path))
