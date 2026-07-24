import json
import shutil
from pathlib import Path
from typing import Optional

import typer
from rich.console import Console

from .compiler import compile_to_terraform
from .models.environment import load_environment

app = typer.Typer(
    help="Docker Compose to Terraform compiler for a PaaS-like experience on AWS.",
)
console = Console()


def version_callback(value: bool):
    if value:
        console.print("composey 0.1.0 (pre-alpha)")
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
    env_file: Path = typer.Option(
        ...,
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
        # 1. Load Environment
        console.print(f"[bold blue]Loading environment:[/] {env_file}")
        env = load_environment(str(env_file))

        # 2. Compile
        console.print(
            f"[bold blue]Compiling:[/] {compose_file} -> {project_name} ({env.target})"
        )
        tf_json = compile_to_terraform(str(compose_file), env, project_name)

        # 3. Write Output
        output_dir.mkdir(parents=True, exist_ok=True)
        output_file = output_dir / "main.tf.json"

        with open(output_file, "w") as f:
            f.write(tf_json)

        # Copy any Docker build contexts next to the manifest so `terraform apply`
        # (run from the output dir) can resolve their relative paths.
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

    except Exception as e:
        console.print(f"[bold red]Error during compilation:[/] {e}")
        raise typer.Exit(code=1)


if __name__ == "__main__":
    app()
