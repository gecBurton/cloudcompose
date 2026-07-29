"""Which database a managed instance actually contains.

`aws_db_instance` carried no `db_name`, so Postgres fell back to an instance
holding one database called "postgres" — and MySQL and MariaDB created none at
all. A compose file that worked locally deployed an instance an application
could reach and had nothing in it to connect to, which surfaces only as an
authentication-shaped error from the driver at runtime.
"""

import pytest

from composey.compiler.inference import infer
from composey.compiler.normalizer import _database_name
from composey.models.environment import AwsEnvironment
from composey.models.semantic import Application, Service


def _env() -> AwsEnvironment:
    return AwsEnvironment(
        name="prod",
        vpc_id="vpc-1",
        public_subnets=["subnet-1"],
        private_subnets=["subnet-2"],
        ecs_cluster_arn="arn:aws:ecs:eu-west-2:1:cluster/c",
    )


def _instance(service: Service) -> dict:
    resources = infer(Application(name="shop", services=[service]), _env())
    return resources.aws_db_instance[f"{service.name}_db"].model_dump()


@pytest.mark.parametrize("image", ["postgres:16", "mysql:8", "mariadb:11"])
def test_every_engine_creates_a_database(image):
    instance = _instance(
        Service(name="db", image=image, capability="database", database_name="shop")
    )

    assert instance["db_name"] == "shop"


def test_the_service_name_is_the_default():
    # Nothing in the compose file named one, so the name a client already uses
    # to reach the service is the one it gets.
    assert _database_name("orders", {}) == "orders"


@pytest.mark.parametrize(
    "variable", ["POSTGRES_DB", "MYSQL_DATABASE", "MARIADB_DATABASE"]
)
def test_a_name_the_compose_file_states_is_honoured(variable):
    # The image would have created this database locally, so an application
    # tested against it connects to that name and no other.
    assert _database_name("db", {variable: "inventory"}) == "inventory"


def test_a_name_only_referenced_is_not_used():
    # docker compose config resolves ${POSTGRES_DB} from a developer's .env,
    # which the parser strips rather than deploy. Nothing is left to honour.
    assert _database_name("db", {}) == "db"


@pytest.mark.parametrize(
    "stated,expected",
    [
        ("my-app", "my_app"),  # hyphens are not valid in either engine
        ("My_App", "my_app"),  # Postgres folds unquoted names to lower
        ("2fast", "fast"),  # Postgres requires a leading letter
        ("_", "app"),  # nothing usable survived
        ("a" * 80, "a" * 63),  # both engines cap the length
    ],
)
def test_a_stated_name_is_coerced_into_one_every_engine_accepts(stated, expected):
    assert _database_name("db", {"POSTGRES_DB": stated}) == expected


def test_a_substituted_service_that_is_not_a_database_has_no_name():
    resources = infer(
        Application(
            name="shop",
            services=[Service(name="cache", image="redis:7", capability="cache")],
        ),
        _env(),
    )

    assert not resources.aws_db_instance
