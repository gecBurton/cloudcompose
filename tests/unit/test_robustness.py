"""IAM least-privilege scoping.

Restored after the Go port deleted normalizer.py (0244d4a). The other half of
this file (missing-image fallback, max_scale string coercion) moved to
composey-go/internal/compiler/normalizer_contract_test.go
(TestNormalizeMissingImageFallsBackToPlaceholder) and the max_scale coercion
fix in models/compose.go's XComposey.UnmarshalJSON, since normalize() no
longer exists here to drive them. What's left is pure inference behavior,
unaffected by the Go port.
"""

import json

from composey.compiler.inference import infer
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application, RateSchedule, Relationship, Service


def test_iam_least_privilege_scoping():
    """
    Ensure that IAM policies are scoped specifically to the resources they need.
    """
    env = AwsEnvironment(
        name="prod",
        vpc_id="vpc-123",
        public_subnets=["s-1"],
        private_subnets=["s-2"],
        ecs_cluster_arn="arn:aws:ecs:us-east-1:123:cluster/my-cluster",
        region="us-east-1",
    )

    app = Application(
        name="myapp",
        services=[
            Service(
                name="job",
                image="img",
                schedule=RateSchedule(value=1, unit="minutes"),
            ),
            # The environment reference is what earns the grant; depends_on
            # alone no longer does.
            Service(name="api", image="img", env={"BUCKET": "blobs"}),
            Service(name="blobs", image="minio/minio", capability="object-storage"),
        ],
        relationships=[Relationship(client="api", server="blobs")],
    )

    resources = infer(app, env)

    # 1. Verify EventBridge IAM Policy for 'job'
    # It should only have permission to run the 'job' task definition
    policy_key = "job_eb_policy"
    assert policy_key in resources.aws_iam_role_policy
    policy = json.loads(resources.aws_iam_role_policy[policy_key].policy)

    run_task_stmt = next(s for s in policy["Statement"] if s["Action"] == "ecs:RunTask")
    assert "${aws_ecs_task_definition.job_td.arn}" in run_task_stmt["Resource"]
    assert "api_td" not in str(run_task_stmt["Resource"])

    # 2. Verify S3 IAM Policy for 'api'
    # It should only have access to the bucket it actually depends on
    s3_policy_key = "api_to_blobs_s3_policy"
    assert s3_policy_key in resources.aws_iam_role_policy
    s3_policy = json.loads(resources.aws_iam_role_policy[s3_policy_key].policy)

    s3_stmt = next(s for s in s3_policy["Statement"] if "s3:*" in s["Action"])
    assert "${aws_s3_bucket.blobs_bucket.arn}" in s3_stmt["Resource"]
