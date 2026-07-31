# Composey: Agent Development Guide

## Project Overview

Composey is a Docker Compose to Terraform compiler that provides a PaaS-like deployment experience for AWS and Azure. It transforms annotated Docker Compose files into cloud infrastructure using intent-based abstractions.

**Architecture**: 4-stage compiler pipeline
1. **Parse**: Sanitize and load Compose files via `docker compose config`
2. **Normalize**: Transform Compose into a cloud-agnostic semantic model
3. **Infer**: Map semantic intent + environment context to cloud resources (AWS/Azure)
4. **Generate**: Produce deterministic, canonical Terraform JSON

**Supported Clouds**:
- **AWS**: ECS Fargate, RDS, ElastiCache, S3, ALB, CloudFront
- **Azure**: Container Apps, PostgreSQL Flexible Server, Blob Storage, Key Vault

## Quick Start

```bash
# Install dependencies
uv sync

# Run the compiler
uv run composey -f examples/flask/compose.yml -e examples/prod.yaml

# Run tests
make test

# Format and lint
make format
```

## Project Structure

```
composey/
├── cli.py                 # CLI entry point (Typer)
├── compiler/
│   ├── __init__.py        # Main compile_application() function
│   ├── parser.py          # Stage 1: Parse Docker Compose
│   ├── normalizer.py      # Stage 2: Normalize to semantic model
│   ├── inference/         # Stage 3: Infer cloud resources
│   │   ├── __init__.py    # AWS inference orchestration
│   │   ├── azure/         # Azure inference (Container Apps, Flexible Server)
│   │   ├── _compute.py    # ECS/Container App resources
│   │   ├── _managed.py    # RDS/Flexible Server resources
│   │   └── ...            # Other inference modules
│   ├── generator.py       # Stage 4a: Generate Terraform JSON for AWS
│   ├── generator_azure.py # Stage 4b: Generate Terraform JSON for Azure
│   ├── connections.py     # Connection string resolution
│   └── explain.py         # Inference reporting (--explain flag)
├── models/
│   ├── compose.py         # Docker Compose models
│   ├── semantic.py        # Cloud-agnostic semantic model
│   ├── environment.py     # Environment configuration models
│   ├── aws.py             # AWS resource models
│   ├── azure.py           # Azure resource models (NEW)
│   └── terraform.py       # Terraform manifest model
├── constants.py           # Centralized constants
└── exceptions.py          # Custom exceptions
```

## Development Guidelines

### Adding New Features

1. **Models First**: Define Pydantic models in appropriate `models/` file
2. **Use `extra="forbid"`**: Prevents typos in configuration
3. **Add Tests**: Unit tests for logic, golden tests for output changes
4. **Update Constants**: Add magic strings to `constants.py`
5. **Use Custom Exceptions**: Raise `ComposeyError` for user-facing errors

### Testing

```bash
# Run all tests
uv run pytest

# Run specific test file
uv run pytest tests/unit/test_normalizer.py

# Run with verbose output
uv run pytest -v

# Run integration tests only
uv run pytest tests/integration/
```

### Code Style

- Use `ruff` for formatting and linting
- Type hints required for function signatures
- Docstrings for public functions
- Prefer explicit over implicit

### Error Handling

```python
from composey.exceptions import ComposeyError, ValidationError

# For user errors
raise ComposeyError(f"Service {name} is invalid because ...")

# For validation errors  
raise ValidationError(f"Invalid x-composey block: {details}")
```

### Key Constants

Located in `composey/constants.py`:
- `DATABASE_DEFAULT_USERNAME` - Default DB username
- `SECRETS_PLACEHOLDER_VALUE` - Placeholder for unset secrets
- `DefaultPorts` - Default ports for managed services
- `SIZE_MAPPINGS` - Compute size configurations

## Common Tasks

### Adding a New AWS Resource

1. Add Pydantic model to `models/aws.py` with `extra="forbid"`
2. Add field to `AWSResources` container class
3. Add generation logic to appropriate `compiler/inference/` module
4. Add unit tests
5. Consider adding to `examples/` for documentation

### Modifying the Semantic Model

1. Update `models/semantic.py`
2. Update `normalizer.py` to produce the new structure
3. Update all inference modules that consume the model
4. Run tests and update golden files if needed

### Adding a CLI Command

1. Modify `cli.py` with new Typer command/option
2. Consider if it needs environment (compile) or not (--explain)
3. Add appropriate error handling
4. Update README.md with usage

## Important Notes

- **Python 3.14+** required for prototype
- **Determinism** is critical - output must be byte-identical for same inputs
- **No Silent Failures** - Unknown keys in x-composey are errors, not ignored
- **AWS-First but Cloud-Agnostic** - Semantic model designed for multi-cloud
- **Terraform is a compilation target** - Never hand-edit generated output

## Debugging

```bash
# Show inference decisions
uv run composey --explain -f docker-compose.yml

# Debug with full traceback
COMPOSEY_DEBUG=1 uv run composey -f compose.yml -e env.yaml
```
