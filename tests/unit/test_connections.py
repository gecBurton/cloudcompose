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
    return resolve_value(value, CONNECTIONS).value


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


def test_userinfo_is_preserved_when_the_service_takes_no_credentials():
    # This connection declares no username, so nothing is authoritative enough
    # to overwrite what the compose file wrote.
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

    assert resolve_value("dbadmin", connections).value == "dbadmin"
    assert resolve_value("http://dbadmin:80", connections).value == "http://dbadmin:80"


# A database that generated its own credentials, which is what inference builds.
CREDENTIALED_DB = Connection(
    host="db.eu-west-2.rds.amazonaws.com",
    port=5432,
    username="composey",
    password="s3cret",
    database="orders",
)
CREDENTIALED = {"db": CREDENTIALED_DB}


def test_the_managed_credentials_replace_what_the_compose_file_wrote():
    # The username in the compose file belonged to a container that no longer
    # exists; the managed instance generated its own. Preserving the local one
    # produces a URL that finds a real database and is rejected by it.
    assert resolve_value("postgres://user:pw@db:5432/app", CREDENTIALED).value == (
        "postgres://composey:s3cret@db.eu-west-2.rds.amazonaws.com:5432/orders"
    )


def test_credentials_are_supplied_even_when_the_url_stated_none():
    assert resolve_value("postgres://db/app", CREDENTIALED).value == (
        "postgres://composey:s3cret@db.eu-west-2.rds.amazonaws.com:5432/orders"
    )


def test_the_deployed_database_name_replaces_the_local_one():
    # Same argument as the port and the credentials: the path named the
    # database the local container created.
    assert resolve_value("postgres://db:5432/whatever", CREDENTIALED).value.endswith(
        "/orders"
    )


def test_query_parameters_survive_the_database_being_substituted():
    assert resolve_value("postgres://db/app?sslmode=require", CREDENTIALED).value == (
        "postgres://composey:s3cret@db.eu-west-2.rds.amazonaws.com:5432/orders"
        "?sslmode=require"
    )


def test_a_value_carrying_a_password_is_confidential():
    assert resolve_value("postgres://db/app", CREDENTIALED).confidential is True


def test_a_bare_reference_is_never_confidential():
    # It resolves to an address, which is not a secret and belongs in the
    # environment where an application can read it cheaply.
    resolution = resolve_value("db", CREDENTIALED)

    assert resolution.confidential is False
    assert resolution.value == CREDENTIALED_DB.host


def test_a_value_reaching_a_service_without_credentials_is_not_confidential():
    assert resolve("redis://cache:6379") is not None
    assert resolve_value("redis://cache:6379", CONNECTIONS).confidential is False


def test_the_service_a_value_reached_for_is_reported():
    # This is what permissions are built from, so it has to be reported rather
    # than guessed at from the resolved value.
    assert resolve_value("http://blobs:9000", CONNECTIONS).service == "blobs"
    assert resolve_value("blobs", CONNECTIONS).service == "blobs"
    assert resolve_value("nothing-to-see", CONNECTIONS).service is None


def test_default_port_prefers_the_connection():
    assert default_port(DB, 80) == 5432
    assert default_port(BUCKET, 443) == 443  # declares no port
    assert default_port(None, 8080) == 8080


def _manifest(env_vars: dict) -> dict:
    """Compile a container that references a database, cache and bucket."""
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
    return json.loads(generate(infer(app, env), env))


def _container(manifest: dict) -> dict:
    import json

    task_def = manifest["resource"]["aws_ecs_task_definition"]["web_td"]
    return json.loads(task_def["container_definitions"])[0]


def _compile(env_vars: dict) -> dict:
    """The plain environment a compiled container is given."""
    container = _container(_manifest(env_vars))
    return {entry["name"]: entry["value"] for entry in container["environment"]}


def _compile_secrets(env_vars: dict) -> dict:
    """The secret references a compiled container is given."""
    container = _container(_manifest(env_vars))
    return {entry["name"]: entry["valueFrom"] for entry in container["secrets"]}


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


def test_a_database_url_never_reaches_the_plain_environment():
    # It carries the master password. In the task definition it would be
    # readable by anyone who can describe it.
    assert "DATABASE_URL" not in _compile(
        {"DATABASE_URL": "postgres://user@db:5432/app"}
    )


def test_a_database_url_is_delivered_as_a_secret():
    secrets = _compile_secrets({"DATABASE_URL": "postgres://user@db:5432/app"})

    assert secrets["DATABASE_URL"] == (
        "${aws_secretsmanager_secret.web_database_url_url.arn}"
    )


def test_the_secret_holds_a_complete_and_usable_url():
    # Host, port, credentials and database all substituted: everything the
    # client needs to connect, assembled by terraform because ECS cannot
    # assemble it (valueFrom takes an ARN, not a template).
    manifest = _manifest({"DATABASE_URL": "postgres://user@db:5432/app"})
    version = manifest["resource"]["aws_secretsmanager_secret_version"][
        "web_database_url_url_v1"
    ]

    assert version["secret_string"] == (
        "postgres://composey:${random_password.db_password.result}"
        "@${aws_db_instance.db_db.address}:5432/db"
    )


def test_a_rotated_password_reaches_the_client():
    # Unlike the secrets composey cannot value, every part of this one is
    # derived from state, so terraform must keep it in step rather than ignore
    # changes to it.
    manifest = _manifest({"DATABASE_URL": "postgres://db/app"})
    version = manifest["resource"]["aws_secretsmanager_secret_version"][
        "web_database_url_url_v1"
    ]

    assert "lifecycle" not in version


def test_the_client_may_read_the_url_secret():
    manifest = _manifest({"DATABASE_URL": "postgres://db/app"})
    policies = manifest["resource"]["aws_iam_role_policy"]

    assert "web_web_database_url_url_policy" in policies


def test_a_url_without_credentials_stays_in_the_environment():
    # ElastiCache takes no credentials, so nothing about this value is secret
    # and paying for a secret to hold it would be waste.
    resolved = _compile({"REDIS_URL": "redis://cache:6379"})

    assert resolved["REDIS_URL"].startswith("redis://${aws_elasticache_cluster")
