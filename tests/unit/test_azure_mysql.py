"""Test Azure MySQL inference."""

from composey.compiler.inference.azure import infer
from composey.models.environment import AzureEnvironment
from composey.models.semantic import Application, Service


def test_mysql_server_is_created():
    """A MySQL image creates an Azure MySQL Flexible Server."""
    env = AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )

    app = Application(
        name="myapp",
        services=[
            Service(
                name="db",
                image="mysql:8.0",
                capability="database",
                size="small",
                database_name="mydb",
            )
        ],
    )

    resources = infer(app, env)

    assert "main" in resources.azurerm_mysql_flexible_server
    server = resources.azurerm_mysql_flexible_server["main"]
    assert server.version == "8.0"
    assert server.sku_name == "B_Standard_B1ms"


def test_mysql_database_is_created():
    """A database is created within the MySQL server."""
    env = AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )

    app = Application(
        name="myapp",
        services=[
            Service(
                name="mysqldb",
                image="mysql:8.0",
                capability="database",
                size="medium",
                database_name="mydb",
            )
        ],
    )

    resources = infer(app, env)

    assert "mysqldb_db" in resources.azurerm_mysql_flexible_database
    db = resources.azurerm_mysql_flexible_database["mysqldb_db"]
    assert db.name == "mydb"
    assert db.charset == "utf8mb4"


def test_postgres_not_confused_with_mysql():
    """PostgreSQL images create PostgreSQL server, not MySQL."""
    env = AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )

    app = Application(
        name="myapp",
        services=[
            Service(
                name="postgres",
                image="postgres:15",
                capability="database",
                size="small",
                database_name="mydb",
            )
        ],
    )

    resources = infer(app, env)

    # Should have PostgreSQL, not MySQL
    assert "main" in resources.azurerm_postgresql_flexible_server
    assert "main" not in resources.azurerm_mysql_flexible_server


def test_both_postgres_and_mysql():
    """Can create both PostgreSQL and MySQL servers in same app."""
    env = AzureEnvironment(
        name="prod",
        region="eastus",
        container_apps_environment_name="prod-env",
        log_analytics_workspace_id="/subscriptions/123/workspaces/prod",
        vnet_id="/subscriptions/123/vnets/prod",
        infrastructure_subnet_id="/subscriptions/123/subnets/prod",
    )

    app = Application(
        name="myapp",
        services=[
            Service(
                name="pg",
                image="postgres:15",
                capability="database",
                size="small",
                database_name="pgdb",
            ),
            Service(
                name="mysql",
                image="mysql:8.0",
                capability="database",
                size="small",
                database_name="mydb",
            ),
        ],
    )

    resources = infer(app, env)

    assert "main" in resources.azurerm_postgresql_flexible_server
    assert "main" in resources.azurerm_mysql_flexible_server
