"""Centralized constants for Composey.

This module contains all magic strings, default values, and configuration
constants used throughout the codebase to avoid duplication and typos.
"""

# =============================================================================
# Version
# =============================================================================
VERSION = "0.1.0"
VERSION_STRING = f"composey {VERSION} (pre-alpha)"

# =============================================================================
# Database Configuration
# =============================================================================
DATABASE_DEFAULT_USERNAME = "composey"
"""Default master username for RDS databases.

'admin' is a reserved master username on RDS Postgres; this is a non-reserved
name valid across every engine composey substitutes.
"""

# The variable each official database image reads to decide which database to
database_name_variables = ("POSTGRES_DB", "MYSQL_DATABASE", "MARIADB_DATABASE")
"""Official database image environment variables for database creation.

Consulted by name, unlike application variables, because these are the images'
documented contract rather than a guess about intent: a compose file setting
POSTGRES_DB has already stated the name it expects.
"""

# Database name sanitization constraints
DATABASE_NAME_MAX_LENGTH = 63
DATABASE_NAME_ALLOWED_CHARS = "_0123456789"

# =============================================================================
# Secrets & Configuration
# =============================================================================
SECRETS_PLACEHOLDER_VALUE = "PLACEHOLDER_VALUE_CHANGE_IN_AWS_CONSOLE"
"""Placeholder value for secrets created without a known value.

Written into every secret composey creates but cannot value. Recognisable in
a console, and obviously not a working credential if one reaches an app.
"""

# =============================================================================
# Networking
# =============================================================================
MAX_NETWORKS_PER_SERVICE = 5
"""Maximum number of security groups AWS attaches to a task's network interface.

A publicly reachable service also gets a dedicated group for the load
balancer, which counts against the same quota.
"""

DEFAULT_NETWORK_NAME = "default"
"""Name of the default network when none is declared."""

# =============================================================================
# ALB Listener Rule Priorities
# =============================================================================
PRIORITY_BANDS = 500
"""Number of priority bands for distributing listener rules across applications."""

BAND_WIDTH = 100
"""Width of each priority band for ordering routes within an application."""

# =============================================================================
# CloudFront & WAF
# =============================================================================
CLOUDFRONT_SCOPE_REGION = "us-east-1"
"""AWS region where CLOUDFRONT-scoped WAF Web ACLs must be created."""

CLOUDFRONT_PROVIDER_ALIAS = "us_east_1"
"""Terraform provider alias for CloudFront/WAF resources."""

CLOUDFRONT_PROVIDER_REF = f"aws.{CLOUDFRONT_PROVIDER_ALIAS}"
"""Full Terraform provider reference for CloudFront-scoped resources."""

ALB_DATA_SOURCE_KEY = "shared_alb"
"""Key of the `data.aws_lb` block used to resolve the shared ALB's DNS name."""


# =============================================================================
# Port Numbers for Managed Services
# =============================================================================
class DefaultPorts:
    """Default ports for managed services once substituted."""

    POSTGRES = 5432
    MYSQL = 3306
    MARIADB = 3306
    REDIS = 6379
    # Azure Managed Redis does not serve on 6379: its database listens on
    # 10000, which is what the connection has to name.
    AZURE_MANAGED_REDIS = 10000

    @classmethod
    def for_database(cls, engine: str) -> int:
        """Get the default port for a database engine."""
        engine_lower = engine.lower()
        if "mysql" in engine_lower:
            return cls.MYSQL
        if "mariadb" in engine_lower:
            return cls.MARIADB
        return cls.POSTGRES


# =============================================================================
# Compute Size Mappings
# =============================================================================
SIZE_MAPPINGS = {
    "small": {"cpu": 256, "memory": 512},
    "medium": {"cpu": 1024, "memory": 2048},
    "large": {"cpu": 4096, "memory": 8192},
}
"""Fargate compute unit mappings for service sizes."""

DB_INSTANCE_CLASSES = {
    "small": "db.t3.micro",
    "medium": "db.t3.medium",
    "large": "db.m5.large",
}
"""RDS instance class mappings for database sizes."""

CACHE_NODE_TYPES = {
    "small": "cache.t3.micro",
    "medium": "cache.t3.medium",
    "large": "cache.m5.large",
}
"""ElastiCache node type mappings for cache sizes."""

# =============================================================================
# Capability Image Detection
# =============================================================================
CAPABILITY_IMAGES: dict[str, frozenset[str]] = {
    "database": frozenset(
        {
            "postgres",
            "postgresql",
            "pgvector",
            "postgis",
            "timescaledb",
            "mysql",
            "mariadb",
            "percona",
            "percona-server-mysql",
        }
    ),
    "cache": frozenset({"redis", "redismod", "redis-stack", "valkey", "keydb"}),
    "object-storage": frozenset({"minio"}),
}
"""Image name patterns that identify service capabilities.

Substrings that identify what a library image really is. Matched against each
path segment of the image reference, so vendored and mirrored images resolve
too: pgvector/pgvector, bitnami/postgresql and public.ecr.aws/.../postgres all
name a database. Matching only the three canonical names missed every one of
those, and the failure was silent — a database ran as a container with its
data directory on ephemeral storage.
"""

# =============================================================================
# Volume Detection
# =============================================================================
BIND_SOURCE_PREFIXES = ("/", "./", "../", "~")
"""Prefixes that mark a short-form volume source as a host path rather than a
named volume, matching how Compose itself disambiguates the two.
"""

# =============================================================================
# Scheduling
# =============================================================================
# Rate expression patterns: "every 30 minutes", "every hour", AWS "rate(1 hour)"
RATE_PATTERN = r"^(?:rate\(\s*|every\s+)(?:(\d+)\s+)?(minute|hour|day)s?\s*\)?$"

# Cron wrapper pattern: AWS "cron(...)" syntax
CRON_WRAPPER_PATTERN = r"^cron\(\s*(.*?)\s*\)$"

# Standard EventBridge defaults
DEFAULT_SCHEDULE_UNIT = "hours"
DEFAULT_LOG_RETENTION_DAYS = 7

# =============================================================================
# IAM Policy Versions
# =============================================================================
IAM_POLICY_VERSION = "2012-10-17"

# =============================================================================
# Log Configuration
# =============================================================================
AWS_LOGS_STREAM_PREFIX = "ecs"

# =============================================================================
# Auto-scaling Thresholds
# =============================================================================
AUTOSCALING_CPU_TARGET = 70.0
AUTOSCALING_MEMORY_TARGET = 80.0
