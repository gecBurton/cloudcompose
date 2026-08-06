"""
Build-from-source: ECR, docker provider wiring, and IAM pull permissions.

Restored after the Go port deleted normalizer.py (0244d4a). The build_context
extraction test moved to composey-go's normalizer_contract_test.go
(TestNormalizeExtractsBuildContext) since that's genuinely normalizer logic.
What's left here is pure inference/generator behavior, unaffected by the Go
port — built directly against the semantic model rather than through the
deleted normalize().
"""

import json

from composey.compiler.generator import generate
from composey.compiler.inference import infer
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application as SemanticApplication
from composey.models.semantic import Service as SemanticService


def _env():
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-123",
        public_subnets=["subnet-1", "subnet-2"],
        private_subnets=["subnet-3", "subnet-4"],
        ecs_cluster_arn="arn:aws:ecs:us-east-1:1:cluster/prod",
        alb_arn="arn:aws:lb:us-east-1:1:loadbalancer/app/shared/1",
        alb_listener_arn="arn:aws:lb:us-east-1:1:listener/app/shared/1/2",
        alb_security_group_id="sg-alb0123456789",
    )


def _build_app():
    return SemanticApplication(
        name="prod",
        services=[
            SemanticService(
                name="web", image="placeholder", port=80, build_context="app"
            )
        ],
    )


def test_infer_provisions_ecr_and_docker_build():
    resources = infer(_build_app(), _env())

    # ECR repo, docker build, and push are all emitted.
    assert "web_ecr" in resources.aws_ecr_repository
    assert resources.docker_image["web_image"].build["context"] == "app"
    assert "web_push" in resources.docker_registry_image

    # The task pulls the pushed digest, not a placeholder image.
    td = resources.aws_ecs_task_definition["web_td"]
    image = json.loads(td.container_definitions)[0]["image"]
    assert image.startswith("${aws_ecr_repository.web_ecr.repository_url}@")
    assert "docker_registry_image.web_push.sha256_digest" in image

    # The execution role gains ECR pull permissions.
    assert "web_exec_ecr_policy" in resources.aws_iam_role_policy


def test_generator_wires_docker_provider_only_when_building():
    env = _env()

    with_build = json.loads(generate(infer(_build_app(), env), env))
    assert "docker" in with_build["terraform"]["required_providers"]
    assert "docker" in with_build["provider"]
    assert "aws_ecr_authorization_token" in with_build["data"]

    # A plain image service does not drag in the docker provider or data block.
    plain = SemanticApplication(
        name="prod",
        services=[SemanticService(name="web", image="nginx", port=80)],
    )
    without_build = json.loads(generate(infer(plain, env), env))
    assert "docker" not in without_build["terraform"]["required_providers"]
    assert without_build.get("data") is None
