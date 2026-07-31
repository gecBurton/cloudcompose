"""Custom exceptions for Composey.

This module defines all custom exceptions used throughout the codebase,
providing clear distinctions between user errors (expected) and system errors
(unexpected), along with helpful error messages and context.
"""


class ComposeyError(Exception):
    """Base exception for all Composey errors.

    User-facing errors should inherit from this class so the CLI can provide
    helpful messages without full stack traces. Unexpected/system errors
    should use standard exceptions.
    """

    def __init__(self, message: str, *, details: str | None = None) -> None:
        super().__init__(message)
        self.message = message
        self.details = details

    def __str__(self) -> str:
        if self.details:
            return f"{self.message}\n\nDetails: {self.details}"
        return self.message


class ValidationError(ComposeyError):
    """Raised when input validation fails.

    Examples:
        - Invalid x-composey block syntax
        - Missing required fields
        - Type mismatches in configuration
    """

    pass


class CompilationError(ComposeyError):
    """Raised when compilation fails for reasons other than validation.

    Examples:
        - Unsupported features
        - Resource conflicts
        - Backend-specific errors
    """

    pass


class InferenceError(ComposeyError):
    """Raised when resource inference fails.

    Examples:
        - Cannot determine service capability
        - Ambiguous configuration
        - Missing environment information
    """

    pass


class ParseError(ComposeyError):
    """Raised when parsing Docker Compose files fails.

    Examples:
        - docker compose config fails
        - Invalid YAML syntax
        - Missing referenced files
    """

    pass


class EnvironmentError(ComposeyError):
    """Raised when environment configuration is invalid.

    Examples:
        - Missing required environment variables
        - Invalid AWS resource ARNs
        - Missing VPC/subnet configuration
    """

    pass


class NetworkError(ComposeyError):
    """Raised when network configuration is invalid.

    Examples:
        - Too many networks attached to a service
        - External networks declared
        - Conflicting network definitions
    """

    pass


class StorageError(ComposeyError):
    """Raised when storage configuration is invalid.

    Examples:
        - Named volumes on container services
        - Unsupported volume types
        - Missing storage configuration
    """

    pass


class IngressError(ComposeyError):
    """Raised when ingress configuration is invalid.

    Examples:
        - Overlapping paths
        - Missing required ALB configuration
        - Invalid listener rules
    """

    pass


class ScheduleError(ComposeyError):
    """Raised when schedule configuration is invalid.

    Examples:
        - Invalid cron expressions
        - Unsupported rate expressions
        - EventBridge constraint violations
    """

    pass
