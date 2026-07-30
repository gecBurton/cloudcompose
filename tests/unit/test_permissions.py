"""Which managed services a client is allowed to reach.

Permissions were granted from `depends_on`, while the endpoints themselves were
injected from the values the compose file carries. The two disagreed whenever a
compose file did one without the other: a service naming a bucket in its
environment and omitting depends_on was handed the bucket's name and denied
every operation on it, and one declaring depends_on without referencing it got
a policy for something it never used.

This is the same mistake networks already corrected — depends_on describes
startup order and constrains nothing — applied to IAM instead of security
groups. Permissions now follow the references, which is the same evidence the
wiring uses.
"""

from composey.compiler.inference import infer
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application, Relationship, Service


def _env() -> AwsEnvironment:
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:eu-west-2:1:cluster/c",
    )


def _policies(client_env: dict, relationships: list[Relationship]) -> set[str]:
    app = Application(
        name="shop",
        services=[
            Service(name="web", image="web", port=80, env=client_env),
            Service(name="blobs", image="minio/minio", capability="object-storage"),
            Service(name="db", image="postgres:16", capability="database"),
        ],
        relationships=relationships,
    )
    return set(infer(app, _env()).aws_iam_role_policy)


def test_a_reference_earns_the_grant_without_depends_on():
    policies = _policies({"BUCKET": "blobs"}, relationships=[])

    assert "web_to_blobs_s3_policy" in policies


def test_depends_on_alone_grants_nothing():
    # Nothing in the environment reaches the bucket, so the service has no way
    # to name it and no business holding a policy for it.
    policies = _policies({}, relationships=[Relationship(client="web", server="blobs")])

    assert "web_to_blobs_s3_policy" not in policies


def test_a_url_reference_earns_the_grant_too():
    # Resolution accepts either shape, so permissions must not be fussier than
    # the wiring that produced them.
    policies = _policies({"S3_ENDPOINT": "http://blobs:9000"}, relationships=[])

    assert "web_to_blobs_s3_policy" in policies


def test_a_database_reference_earns_access_to_its_credentials():
    policies = _policies({"DB_HOST": "db"}, relationships=[])

    assert "web_to_db_rds_secret" in policies


def test_an_unreferenced_database_grants_nothing():
    policies = _policies({}, relationships=[Relationship(client="web", server="db")])

    assert "web_to_db_rds_secret" not in policies


def test_a_grant_is_scoped_to_the_service_actually_referenced():
    app = Application(
        name="shop",
        services=[
            Service(name="web", image="web", port=80, env={"BUCKET": "blobs"}),
            Service(name="blobs", image="minio/minio", capability="object-storage"),
            Service(name="other", image="minio/minio", capability="object-storage"),
        ],
        relationships=[Relationship(client="web", server="other")],
    )
    policies = set(infer(app, _env()).aws_iam_role_policy)

    assert "web_to_blobs_s3_policy" in policies
    assert "web_to_other_s3_policy" not in policies
