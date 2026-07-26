"""Whether tearing a stack down keeps what it held.

Every stateful resource composey creates was set to discard: RDS skipped its
final snapshot, buckets force-destroyed with their contents, secrets hard-deleted
and ECR repositories emptied. Convenient for a pre-alpha project testing against
a sandbox, and silently destructive anywhere else — `terraform destroy` took the
database with it and left nothing to restore from.
"""

import json

import pytest

from composey.compiler import compile_to_terraform
from composey.models.environment import AwsEnvironment


def _resources(retain: bool) -> dict:
    env = AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:eu-west-2:1:cluster/c",
        retain_data_on_destroy=retain,
    )
    return json.loads(
        compile_to_terraform("examples/doctor/compose.yml", env, "doctor")
    )["resource"]


def test_retaining_is_the_default():
    env = AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:eu-west-2:1:cluster/c",
    )

    assert env.retain_data_on_destroy is True


def test_a_retained_database_takes_a_final_snapshot():
    database = _resources(retain=True)["aws_db_instance"]["db_db"]

    assert database["skip_final_snapshot"] is False
    assert database["final_snapshot_identifier"]


def test_the_snapshot_name_is_unique_per_teardown():
    # A fixed name collides on the second destroy, because the snapshot the
    # first one wrote still exists.
    resources = _resources(retain=True)

    assert "db_snapshot" in resources["random_id"]
    assert (
        "${random_id.db_snapshot.hex}"
        in resources["aws_db_instance"]["db_db"]["final_snapshot_identifier"]
    )


@pytest.mark.parametrize(
    "kind,key,expected",
    [
        ("aws_s3_bucket", "force_destroy", False),
        ("aws_ecr_repository", "force_delete", False),
    ],
)
def test_retained_resources_do_not_discard(kind, key, expected):
    resources = _resources(retain=True)

    assert all(r[key] == expected for r in resources[kind].values())


@pytest.mark.parametrize(
    "kind,key,expected",
    [
        ("aws_s3_bucket", "force_destroy", True),
        ("aws_ecr_repository", "force_delete", True),
    ],
)
def test_a_throwaway_environment_discards_everything(kind, key, expected):
    # The acceptance smoke test relies on this: teardown must not stop at a
    # non-empty bucket or a secret whose name is still reserved.
    resources = _resources(retain=False)

    assert all(r[key] == expected for r in resources[kind].values())


def test_secrets_are_always_hard_deleted():
    # A recovery window keeps the name reserved and blocks re-creating a secret
    # with the same name, which broke a real deploy. The window would protect a
    # value an operator can re-enter, and a retained database is recoverable
    # from its snapshot regardless, so retention does not buy enough to reopen
    # that failure.
    for retain in (True, False):
        secrets = _resources(retain)["aws_secretsmanager_secret"].values()
        assert all(s["recovery_window_in_days"] == 0 for s in secrets)


def test_a_throwaway_database_skips_its_snapshot():
    database = _resources(retain=False)["aws_db_instance"]["db_db"]

    assert database["skip_final_snapshot"] is True
    assert "final_snapshot_identifier" not in database
    assert "random_id" not in _resources(retain=False)
