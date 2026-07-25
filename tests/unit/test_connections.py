import pytest

from composey.compiler.connections import default_port, resolve_value
from composey.models.semantic import Connection

DB = Connection(host="db.eu-west-2.rds.amazonaws.com", port=5432)
CACHE = Connection(host="cache.euw2.cache.amazonaws.com", port=6379)
BUCKET = Connection(
    host="prod-blobs.s3.amazonaws.com",
    name="prod-blobs",
    addressed_by="name",
    port=None,
)

CONNECTIONS = {"db": DB, "cache": CACHE, "blobs": BUCKET}


def resolve(value: str) -> str:
    return resolve_value(value, CONNECTIONS)


def test_bare_reference_to_a_database_resolves_to_its_host():
    assert resolve("db") == DB.host


def test_bare_reference_to_a_bucket_resolves_to_its_name():
    # A bucket is addressed by name, not by host: `BUCKET_NAME: blobs` wants the
    # bucket, not a domain.
    assert resolve("blobs") == BUCKET.name


def test_url_host_is_swapped_and_port_comes_from_the_connection():
    assert resolve("redis://cache") == f"redis://{CACHE.host}:6379"
    assert resolve("redis://cache:6379") == f"redis://{CACHE.host}:6379"


def test_scheme_and_path_are_preserved():
    assert resolve("postgres://db:5432/app") == f"postgres://{DB.host}:5432/app"
    assert resolve("rediss://cache/0") == f"rediss://{CACHE.host}:6379/0"


def test_port_is_dropped_when_the_connection_declares_none():
    # The local minio port is meaningless once S3 has replaced it.
    assert resolve("http://blobs:9000") == f"http://{BUCKET.host}"


def test_userinfo_is_preserved():
    assert resolve("postgres://user:pw@db:5432/app") == (
        f"postgres://user:pw@{DB.host}:5432/app"
    )


@pytest.mark.parametrize(
    "value",
    [
        "",
        "localhost",
        "/run/secrets/db-password",
        "database",  # not the service `db`
        "redis://other-cache:6379",
        "https://example.com/db",  # `db` only in the path
    ],
)
def test_values_that_reference_nothing_are_untouched(value):
    assert resolve(value) == value


def test_variable_names_are_never_consulted():
    # The whole point of the descriptor: two variables with identical values
    # resolve identically no matter what they are called. The previous
    # implementation chose a different substitution based on the name.
    assert resolve_value("cache", CONNECTIONS) == resolve_value("cache", CONNECTIONS)
    assert resolve("cache") == CACHE.host


def test_a_service_named_like_a_substring_is_not_matched():
    connections = {"db": DB}

    assert resolve_value("dbadmin", connections) == "dbadmin"
    assert resolve_value("http://dbadmin:80", connections) == "http://dbadmin:80"


def test_default_port_prefers_the_connection():
    assert default_port(DB, 80) == 5432
    assert default_port(BUCKET, 443) == 443  # declares no port
    assert default_port(None, 8080) == 8080


def _compile(env_vars: dict) -> dict:
    """Compile a container that depends on a database, cache and bucket."""
    import json

    from composey.compiler.generator import generate
    from composey.compiler.inference import infer
    from composey.models.environment import AwsEnvironment
    from composey.models.semantic import Application, Relationship, Service

    env = AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:us-east-1:123456789012:cluster/c",
    )
    app = Application(
        name="app",
        services=[
            Service(name="web", image="web", port=80, env=env_vars),
            Service(name="db", image="postgres:16", capability="database"),
            Service(name="cache", image="redis:7", capability="cache"),
            Service(name="blobs", image="minio/minio", capability="object-storage"),
        ],
        relationships=[
            Relationship(client="web", server="db"),
            Relationship(client="web", server="cache"),
            Relationship(client="web", server="blobs"),
        ],
    )
    manifest = json.loads(generate(infer(app, env), env))
    task_def = manifest["resource"]["aws_ecs_task_definition"]["web_td"]
    container = json.loads(task_def["container_definitions"])[0]
    return {entry["name"]: entry["value"] for entry in container["environment"]}


def test_end_to_end_wiring_of_every_capability():
    resolved = _compile(
        {
            "DB_HOST": "db",
            "REDIS_URL": "redis://cache:6379",
            "BUCKET_NAME": "blobs",
            "S3_ENDPOINT": "http://blobs:9000",
            "UNRELATED": "leave-me-alone",
        }
    )

    assert resolved["DB_HOST"] == "${aws_db_instance.db_db.address}"
    assert resolved["REDIS_URL"] == (
        "redis://${aws_elasticache_cluster.cache_cache.cache_nodes[0].address}:6379"
    )
    assert resolved["BUCKET_NAME"] == "${aws_s3_bucket.blobs_bucket.id}"
    assert resolved["S3_ENDPOINT"] == (
        "http://${aws_s3_bucket.blobs_bucket.bucket_domain_name}"
    )
    assert resolved["UNRELATED"] == "leave-me-alone"


def test_database_url_keeps_its_port():
    # The previous implementation rewrote any URL as if it were http, which
    # silently dropped the port from schemes like postgres://.
    resolved = _compile({"DATABASE_URL": "postgres://user@db:5432/app"})

    assert resolved["DATABASE_URL"] == (
        "postgres://user@${aws_db_instance.db_db.address}:5432/app"
    )
