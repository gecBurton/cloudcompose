import json
import os
import shutil
from pathlib import Path
from typing import Optional

import typer
from importlib.metadata import version
from rich.console import Console

from .cli_env import register_init_commands
from .compiler import compile_application
from .compiler.explain import explain, render
from .compiler.normalizer import normalize
from .compiler.parser import parse
from .exceptions import ComposeyError
from .models.environment import load_environment

app = typer.Typer(
    help="Docker Compose to Terraform compiler for a PaaS-like experience on AWS.",
)
console = Console()

# Register environment initialization commands
register_init_commands(app)


def _get_version() -> str:
    """Get the version from package metadata."""
    try:
        return version("composey")
    except Exception:
        return "unknown"


def version_callback(value: bool):
    if value:
        console.print(f"composey {_get_version()} (pre-alpha)")
        raise typer.Exit()


@app.command()
def main(
    compose_file: Path = typer.Option(
        "compose.yml",
        "--file",
        "-f",
        help="Path to the Docker Compose file",
        exists=True,
        file_okay=True,
        dir_okay=False,
        readable=True,
    ),
    env_file: Optional[Path] = typer.Option(
        None,
        "--env",
        "-e",
        help="Path to the Environment configuration YAML",
        exists=True,
        file_okay=True,
        dir_okay=False,
        readable=True,
    ),
    project_name: Optional[str] = typer.Option(
        None,
        "--project",
        "-p",
        help="Name of the project (defaults to the directory name)",
    ),
    output_dir: Path = typer.Option(
        "terraform",
        "--out",
        "-o",
        help="Directory to write the generated Terraform JSON",
    ),
    explain_only: bool = typer.Option(
        False,
        "--explain",
        help="Report every inference the compiler makes, and write nothing",
    ),
    version: Optional[bool] = typer.Option(
        None,
        "--version",
        "-v",
        callback=version_callback,
        is_eager=True,
        help="Show the version and exit.",
    ),
):
    """
    Compile a Docker Compose file into deterministic Terraform JSON.
    """
    if project_name is None:
        project_name = compose_file.absolute().parent.name

    try:
        # Explaining needs no environment: every inference reported here is made
        # before the target is consulted.
        if explain_only:
            docker_app = parse(str(compose_file))
            semantic = normalize(docker_app, project_name)
            console.print(render(explain(docker_app, semantic)))
            return

        if env_file is None:
            console.print("[bold red]Error:[/] --env is required to compile")
            raise typer.Exit(code=1)

        # 1. Load Environment
        console.print(f"[bold blue]Loading environment:[/] {env_file}")
        env = load_environment(str(env_file))

        # 2. Compile
        console.print(
            f"[bold blue]Compiling:[/] {compose_file} -> {project_name} ({env.target})"
        )
        docker_app = parse(str(compose_file))
        semantic = normalize(docker_app, project_name)

        # Report anything the compiler could not decide.
        warnings = [d for d in explain(docker_app, semantic) if d.source == "warning"]
        for warning in warnings:
            console.print(f"[yellow]warning[/] {warning.subject}: {warning.decision}")
        if warnings:
            console.print(
                f"[yellow]{len(warnings)} warning(s)[/] — run with --explain for detail"
            )

        tf_json = compile_application(semantic, env)

        # 3. Write Output
        output_dir.mkdir(parents=True, exist_ok=True)
        output_file = output_dir / "main.tf.json"

        with open(output_file, "w") as f:
            f.write(tf_json)

        # Copy any Docker build contexts next to the manifest
        compose_dir = compose_file.absolute().parent
        docker_images = json.loads(tf_json).get("resource", {}).get("docker_image", {})
        for image in docker_images.values():
            context = image.get("build", {}).get("context")
            if not context:
                continue
            src = compose_dir / context
            dst = output_dir / context
            if src.is_dir():
                shutil.copytree(src, dst, dirs_exist_ok=True)
                console.print(f"[bold blue]Copied build context:[/] {context}")

        console.print(
            f"[bold green]Success![/] Terraform manifest written to [cyan]{output_file}[/]"
        )

    except typer.Exit:
        raise
    except ComposeyError as e:
        # User-facing errors: show clean message without stack trace
        console.print(f"[bold red]Error:[/] {e.message}")
        if e.details:
            console.print(f"[dim]{e.details}[/]")
        raise typer.Exit(code=1)
    except Exception as e:
        # Unexpected errors: show message, optionally full traceback
        console.print(f"[bold red]Unexpected error:[/] {e}")
        if os.getenv("COMPOSEY_DEBUG"):
            raise
        raise typer.Exit(code=1)


if __name__ == "__main__":
    app()
