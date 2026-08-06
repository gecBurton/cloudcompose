"""Values from env_file and ${...} name a variable; they do not value it.

`docker compose config` folds both into the environment, so compiling a real
project baked POSTGRES_PASSWORD, SENTRY_DSN and GITHUB_TOKEN into the Terraform
manifest as plaintext — alongside ENVIRONMENT=local and POSTGRES_HOST=localhost,
which are not secret but are simply wrong once deployed.

Restored after the Go port replaced parser.py/normalizer.py with the Go
binary (0244d4a). The literal-vs-referenced environment split this exercises
is now Go's splitEnvironment/declaredEnvironment
(composey-go/internal/compiler/parser.go) rather than Python's
_split_environment — parse_and_normalize_go takes the place of parse()+
normalize(). explain() no longer receives a real docker_app on this path (see
explain.py's docker_app=None branch), so the report-content test below
exercises the semantic-model-only decisions that branch is able to make
rather than the docker_app-dependent ones.
"""

import json
import textwrap

import pytest

from composey.compiler import compile_to_terraform
from composey.compiler.explain import explain
from composey.compiler.hybrid import parse_and_normalize_go
from composey.models.environment import AwsEnvironment


@pytest.fixture
def project(tmp_path):
    def build(compose: str, env_file: str = "") -> tuple:
        (tmp_path / ".env").write_text(textwrap.dedent(env_file))
        path = tmp_path / "compose.yml"
        path.write_text(textwrap.dedent(compose))
        app = parse_and_normalize_go(str(path), "app")
        return app, path

    return build


def _env() -> AwsEnvironment:
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:eu-west-2:1:cluster/c",
    )


LITERAL_AND_ENV_FILE = """
    services:
      web:
        image: web
        env_file: [.env]
        environment:
          LOG_LEVEL: debug
          FROM_SHELL: ${SOME_TOKEN}
"""

SECRETS_DOT_ENV = """
    SOME_TOKEN=shhh
    POSTGRES_PASSWORD=insecure
    SENTRY_DSN=https://key@sentry.example.com/1
"""


def test_literal_values_cross_over(project):
    app, _ = project(LITERAL_AND_ENV_FILE, SECRETS_DOT_ENV)

    assert app.services[0].env == {"LOG_LEVEL": "debug"}


def test_env_file_values_do_not_cross_over(project):
    app, _ = project(LITERAL_AND_ENV_FILE, SECRETS_DOT_ENV)

    assert "POSTGRES_PASSWORD" in app.services[0].config
    assert "insecure" not in json.dumps(app.services[0].model_dump())


def test_interpolated_values_do_not_cross_over(project):
    # `FROM_SHELL: ${SOME_TOKEN}` is written in the compose file, but its value
    # is not — it comes from the same untrusted place as env_file.
    app, _ = project(LITERAL_AND_ENV_FILE, SECRETS_DOT_ENV)

    assert "FROM_SHELL" in app.services[0].config
    assert "shhh" not in json.dumps(app.services[0].model_dump())


def test_secrets_never_reach_the_manifest(project):
    _, path = project(LITERAL_AND_ENV_FILE, SECRETS_DOT_ENV)

    manifest = compile_to_terraform(str(path), _env(), "app")

    for secret in ("insecure", "shhh", "sentry.example.com"):
        assert secret not in manifest


def test_platform_values_arrive_as_secret_references(project):
    _, path = project(LITERAL_AND_ENV_FILE, SECRETS_DOT_ENV)

    manifest = json.loads(compile_to_terraform(str(path), _env(), "app"))
    container = json.loads(
        manifest["resource"]["aws_ecs_task_definition"]["web_td"][
            "container_definitions"
        ]
    )[0]

    references = {s["name"]: s["valueFrom"] for s in container["secrets"]}
    assert "POSTGRES_PASSWORD" in references
    assert references["POSTGRES_PASSWORD"].endswith(":POSTGRES_PASSWORD::")
    assert [e["name"] for e in container["environment"]] == ["LOG_LEVEL"]


def test_one_secret_holds_every_platform_value(project):
    # Secrets Manager bills per secret, and ECS can select a key from a JSON one.
    _, path = project(LITERAL_AND_ENV_FILE, SECRETS_DOT_ENV)

    manifest = json.loads(compile_to_terraform(str(path), _env(), "app"))

    assert list(manifest["resource"]["aws_secretsmanager_secret"]) == ["web_config"]


def test_placeholders_are_not_reapplied_on_later_runs(project):
    _, path = project(LITERAL_AND_ENV_FILE, SECRETS_DOT_ENV)

    manifest = json.loads(compile_to_terraform(str(path), _env(), "app"))
    version = manifest["resource"]["aws_secretsmanager_secret_version"]["web_config_v1"]

    assert version["lifecycle"] == {"ignore_changes": ["secret_string"]}


def test_the_report_names_what_must_be_set(project):
    app, _ = project(LITERAL_AND_ENV_FILE, SECRETS_DOT_ENV)

    warnings = [d for d in explain(None, app) if d.source == "warning"]

    assert any("need values from the platform" in d.decision for d in warnings)


def test_a_service_with_no_env_file_is_unaffected(project):
    app, _ = project("""
        services:
          web:
            image: web
            environment:
              LOG_LEVEL: debug
    """)

    assert app.services[0].env == {"LOG_LEVEL": "debug"}
    assert app.services[0].config == []
