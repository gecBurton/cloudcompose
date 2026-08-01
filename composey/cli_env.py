"""CLI commands for environment management (`composey init env`).

This module provides commands for initializing shared infrastructure environments
that multiple applications can deploy to.
"""

import json
from pathlib import Path
from typing import Optional

import typer
from rich.console import Console

from .environment_generator import (
    generate_aws_environment,
    generate_azure_environment,
    generate_gcp_environment,
)
from .exceptions import ComposeyError

console = Console()


def register_init_commands(app: typer.Typer) -> None:
    """Register environment initialization commands with the main CLI app."""

    @app.command(name="init")
    def init_env(
        provider: str = typer.Option(
            "aws",
            "--provider",
            "-p",
            help="Cloud provider (aws, azure, gcp)",
        ),
        name: str = typer.Option(
            ...,
            "--name",
            "-n",
            help="Environment name (e.g., prod, staging, dev)",
        ),
        region: Optional[str] = typer.Option(
            None,
            "--region",
            "-r",
            help="Cloud region (default: eu-west-2 for AWS, eastus for Azure, us-central1 for GCP)",
        ),
        output: Optional[Path] = typer.Option(
            None,
            "--output",
            "-o",
            help="Output directory for generated files",
        ),
        vpc_cidr: str = typer.Option(
            "10.0.0.0/16",
            "--vpc-cidr",
            help="CIDR block for the VPC/VNet",
        ),
        az_count: int = typer.Option(
            2,
            "--az-count",
            help="Number of availability zones (AWS only)",
        ),
        create_alb: bool = typer.Option(
            True,
            "--create-alb/--no-alb",
            help="Create a shared ALB (AWS only)",
        ),
        certificate_arn: Optional[str] = typer.Option(
            None,
            "--certificate-arn",
            help="ACM certificate ARN for HTTPS (AWS only)",
        ),
        aws_endpoint: Optional[str] = typer.Option(
            None,
            "--aws-endpoint",
            help="Custom endpoint for AWS services (e.g., LocalStack)",
        ),
        azure_endpoint: Optional[str] = typer.Option(
            None,
            "--azure-endpoint",
            help="Custom endpoint for Azure services",
        ),
        gcp_endpoint: Optional[str] = typer.Option(
            None,
            "--gcp-endpoint",
            help="Custom endpoint for GCP services",
        ),
        retain_data: bool = typer.Option(
            True,
            "--retain-data/--no-retain-data",
            help="Whether destroying the stack preserves data (snapshots, etc.)",
        ),
        tags: Optional[str] = typer.Option(
            None,
            "--tags",
            help='Tags as JSON object (e.g., \'{"Team": "platform"}\')',
        ),
    ):
        """
        Initialize a shared infrastructure environment.

        Creates the VPC, subnets, ALB/Container Apps Environment, and other shared resources
        that multiple applications can use. This is typically run once by a
        platform team, and then developers deploy apps with `composey up`.

        Examples:

            # AWS: Create a production environment with defaults
            composey init --name prod

            # Azure: Create a staging environment
            composey init --provider azure --name staging --region eastus

            # GCP: Create a dev environment
            composey init --provider gcp --name dev --region us-central1

            # AWS: Create with HTTPS (requires ACM certificate)
            composey init --name prod --certificate-arn arn:aws:acm:...
        """
        # Validate provider
        supported_providers = ["aws", "azure", "gcp"]
        provider_lower = provider.lower()
        if provider_lower not in supported_providers:
            console.print(
                f"[bold red]Error:[/] Provider '{provider}' is not supported. "
                f"Supported: {', '.join(supported_providers)}"
            )
            raise typer.Exit(code=1)

        # Set defaults based on provider
        if region is None:
            if provider_lower == "aws":
                region = "eu-west-2"
            elif provider_lower == "azure":
                region = "eastus"
            elif provider_lower == "gcp":
                region = "us-central1"

        # Determine output directory
        if output is None:
            output = Path(f"{name}-infrastructure")

        # Parse tags if provided
        parsed_tags = {}
        if tags:
            try:
                parsed_tags = json.loads(tags)
            except json.JSONDecodeError:
                console.print(
                    '[bold red]Error:[/] Invalid JSON in --tags. Use format: \'{"Key": "Value"}\''
                )
                raise typer.Exit(code=1)

        console.print(f"[bold blue]Initializing {provider} environment:[/] {name}")
        console.print(f"[dim]Region:[/] {region}")
        console.print(f"[dim]Output:[/] {output}")
        console.print(f"[dim]VPC CIDR:[/] {vpc_cidr}")
        if provider_lower == "aws":
            console.print(f"[dim]AZ Count:[/] {az_count}")
            console.print(f"[dim]Create ALB:[/] {create_alb}")

        try:
            # Generate Terraform for shared infrastructure
            if provider_lower == "aws":
                terraform_json = generate_aws_environment(
                    name=name,
                    region=region,
                    vpc_cidr=vpc_cidr,
                    az_count=az_count,
                    create_alb=create_alb,
                    certificate_arn=certificate_arn,
                    aws_endpoint=aws_endpoint,
                    tags=parsed_tags,
                    retain_data_on_destroy=retain_data,
                )
            elif provider_lower == "azure":
                terraform_json = generate_azure_environment(
                    name=name,
                    location=region,
                    vnet_cidr=vpc_cidr,
                    tags=parsed_tags,
                    retain_data_on_destroy=retain_data,
                )
            elif provider_lower == "gcp":
                terraform_json = generate_gcp_environment(
                    name=name,
                    region=region,
                    vpc_cidr=vpc_cidr,
                    tags=parsed_tags,
                    retain_data_on_destroy=retain_data,
                )
            else:
                raise ComposeyError(
                    f"Provider '{provider}' generation not implemented",
                    details="This provider is not yet supported for environment initialization.",
                )

            # Create output directory
            output.mkdir(parents=True, exist_ok=True)

            # Write Terraform manifest
            tf_file = output / "main.tf.json"
            with open(tf_file, "w") as f:
                f.write(terraform_json)

            console.print("[bold green]Success![/] Environment initialized.")
            console.print()
            console.print("[bold]Generated files:[/]")
            console.print(
                f"  [cyan]{tf_file}[/] - Terraform manifest for shared infrastructure"
            )
            console.print(
                "  [cyan]environment.yml[/] - Will be created by Terraform apply"
            )
            console.print()
            console.print("[bold]Next steps:[/]")
            console.print(f"  1. cd {output}")
            console.print("  2. terraform init")
            console.print("  3. terraform apply")
            console.print()
            console.print("[bold]Deploy an app:[/]")
            console.print(f"  composey up --env {output}/environment.yml")

        except ComposeyError as e:
            console.print(f"[bold red]Error:[/] {e.message}")
            if e.details:
                console.print(f"[dim]{e.details}[/]")
            raise typer.Exit(code=1)
        except Exception as e:
            console.print(f"[bold red]Unexpected error:[/] {e}")
            raise typer.Exit(code=1)
