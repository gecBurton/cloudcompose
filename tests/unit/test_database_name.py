"""Which database a managed instance actually contains.

`aws_db_instance` carried no `db_name`, so Postgres fell back to an instance
holding one database called "postgres" — and MySQL and MariaDB created none at
all. A compose file that worked locally deployed an instance an application
could reach and had nothing in it to connect to, which surfaces only as an
authentication-shaped error from the driver at runtime.

Restored after the Go port deleted normalizer.py (0244d4a). The
_database_name derivation/sanitization tests moved to
composey-go/internal/compiler/normalizer_contract_test.go, since
_database_name no longer exists here to import. What's left is pure
inference behavior and semantic-model validation, both unaffected by the Go
port.
"""

import pytest
from pydantic import ValidationError

from composey.compiler.inference import infer
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


def test_a_database_must_carry_a_name():
    # The invariant lives on the model so no backend can reach for a default of
    # its own and land back on the reserved word.
    with pytest.raises(ValidationError, match="must carry a database_name"):
        Service(name="db", image="postgres:16", capability="database")


def test_a_container_needs_no_database_name():
    assert Service(name="web", image="nginx").database_name is None


def test_a_substituted_service_that_is_not_a_database_has_no_name():
    resources = infer(
        Application(
            name="shop",
            services=[Service(name="cache", image="redis:7", capability="cache")],
        ),
        _env(),
    )

    assert not resources.aws_db_instance
