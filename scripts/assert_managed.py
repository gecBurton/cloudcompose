#!/usr/bin/env python3
"""Assert composey's managed-service substitutions actually landed in AWS.

Reads `terraform show -json` output (the applied state, so every resource here
really exists in AWS) on stdin and, for each substitution the example uses,
verifies:

  1. the managed resource was created (the service was substituted);
  2. no container is running the original image (substitution, not addition);
  3. the DEPLOYED task definition resolved the endpoint to the real address
     (proves injection reached the running container, not just the plan).

Point 2 is the one an HTTP health check cannot make: an app that talks to a
postgres *sidecar* is just as healthy as one talking to RDS, so connectivity
alone never proves the substitution happened.

Covers minio->S3, postgres/mysql/mariadb->RDS and redis->ElastiCache. Sections
for substitutions the example does not use are skipped, so this is a no-op for
an example with no managed services.

Exits 0 on success (printing a summary) or 1 with a FAIL message.
"""

import json
import sys


def managed_resources(state):
    """Yield every managed resource across all modules in the state tree."""
    stack = [state.get("values", {}).get("root_module", {})]
    while stack:
        mod = stack.pop()
        yield from mod.get("resources", [])
        stack.extend(mod.get("child_modules", []))


def fail(msg):
    print(f"\nASSERT FAIL: {msg}", file=sys.stderr)
    sys.exit(1)


def containers(by_type):
    """Yield every container definition across all deployed task definitions."""
    for td in by_type.get("aws_ecs_task_definition", []):
        for container in json.loads(td["values"]["container_definitions"]):
            yield container


def assert_not_containerised(by_type, images, label):
    """Fail if any deployed container still runs one of the original images."""
    for container in containers(by_type):
        image = container.get("image", "").lower()
        if any(name in image for name in images):
            fail(f"{label} is running as a container ({image}) — not substituted")
    print(f"  [ok] no {label} container — substituted, not added")


def env_values(by_type):
    """Yield (name, value) for every environment variable of every container."""
    for container in containers(by_type):
        for entry in container.get("environment", []):
            yield entry["name"], entry["value"]


def assert_injected(by_type, address, label):
    """Fail unless some container env var carries the real endpoint address."""
    for name, value in env_values(by_type):
        if address in value:
            print(f"  [ok] {label} endpoint injected into {name}: {value}")
            return
    declared = ", ".join(sorted(name for name, _ in env_values(by_type))) or "none"
    fail(
        f"no environment variable contains the {label} address {address!r} — "
        f"endpoint injection did not reach the deployed task (saw: {declared})"
    )


def check_s3(by_type):
    buckets = by_type.get("aws_s3_bucket", [])
    if not buckets:
        return False

    print("\nS3 (minio substitution):")
    bucket_names = {b["values"]["bucket"] for b in buckets}
    bucket_domains = {b["values"].get("bucket_domain_name", "") for b in buckets}
    print(f"  [ok] S3 bucket created: {', '.join(sorted(bucket_names))}")

    assert_not_containerised(by_type, ["minio"], "minio")

    s3_policy = None
    for policy in by_type.get("aws_iam_role_policy", []):
        if "s3:" in policy["values"].get("policy", ""):
            s3_policy = policy["values"]["name"]
            break
    if not s3_policy:
        fail("no IAM role policy granting s3 access")
    print(f"  [ok] IAM policy grants S3 access: {s3_policy}")

    resolved_bucket = resolved_endpoint = None
    for name, value in env_values(by_type):
        if name == "BUCKET_NAME":
            resolved_bucket = value
        if name == "S3_ENDPOINT":
            resolved_endpoint = value

    if resolved_bucket is None:
        fail("BUCKET_NAME not found in any deployed task definition")
    if "${" in resolved_bucket:
        fail(f"BUCKET_NAME never resolved (still a reference): {resolved_bucket}")
    if resolved_bucket not in bucket_names:
        fail(f"BUCKET_NAME={resolved_bucket!r} is not the real bucket {bucket_names}")
    print(f"  [ok] BUCKET_NAME injected with real bucket: {resolved_bucket}")

    # S3_ENDPOINT is optional: apps hitting real S3 via the SDK don't need it.
    # Only assert it resolved correctly when the app actually declares it.
    if resolved_endpoint is None:
        print("  [ok] S3_ENDPOINT not declared (app uses the default S3 endpoint)")
    else:
        if "${" in resolved_endpoint:
            fail(f"S3_ENDPOINT declared but not resolved: {resolved_endpoint!r}")
        if not any(dom and dom in resolved_endpoint for dom in bucket_domains):
            fail(
                f"S3_ENDPOINT={resolved_endpoint!r} does not contain the bucket "
                f"domain {bucket_domains}"
            )
        print(f"  [ok] S3_ENDPOINT injected with real domain: {resolved_endpoint}")

    return True


def check_rds(by_type):
    instances = by_type.get("aws_db_instance", [])
    if not instances:
        return False

    print("\nRDS (database substitution):")
    for instance in instances:
        values = instance["values"]
        print(
            f"  [ok] RDS instance created: {values['identifier']} "
            f"({values['engine']} on {values['instance_class']})"
        )

    assert_not_containerised(by_type, ["postgres", "mysql", "mariadb"], "database")

    address = instances[0]["values"].get("address") or ""
    if not address or "${" in address:
        fail(f"RDS instance has no resolved address: {address!r}")
    assert_injected(by_type, address, "RDS")

    # Credentials must reach the container from Secrets Manager, never as a
    # plaintext environment variable.
    secret_arns = {
        s["values"]["arn"] for s in by_type.get("aws_secretsmanager_secret", [])
    }
    injected = [
        entry
        for container in containers(by_type)
        for entry in container.get("secrets", [])
        if any(entry["valueFrom"].startswith(arn) for arn in secret_arns)
    ]
    if not injected:
        fail("no container reads database credentials from Secrets Manager")
    names = ", ".join(sorted(entry["name"] for entry in injected))
    print(f"  [ok] credentials injected from Secrets Manager: {names}")

    # Warn rather than fail: a compose file may legitimately set a *_PASSWORD
    # variable to a file path or a placeholder, and that is the author's choice,
    # not something composey introduced. Failing the deployment on a heuristic
    # this loose would block real runs for the wrong reason.
    plaintext = [
        f"{name}={value!r}"
        for name, value in env_values(by_type)
        if "PASSWORD" in name.upper()
    ]
    if plaintext:
        print(f"  [warn] plaintext *_PASSWORD env vars present: {', '.join(plaintext)}")
    else:
        print("  [ok] no plaintext password in the task environment")

    return True


def check_cache(by_type):
    clusters = by_type.get("aws_elasticache_cluster", [])
    if not clusters:
        return False

    print("\nElastiCache (redis substitution):")
    for cluster in clusters:
        values = cluster["values"]
        print(
            f"  [ok] ElastiCache cluster created: {values['cluster_id']} "
            f"({values['engine']} on {values['node_type']})"
        )

    assert_not_containerised(by_type, ["redis", "valkey"], "redis")

    nodes = clusters[0]["values"].get("cache_nodes") or []
    if not nodes:
        fail("ElastiCache cluster reports no cache nodes")
    address = nodes[0].get("address") or ""
    if not address or "${" in address:
        fail(f"ElastiCache node has no resolved address: {address!r}")
    assert_injected(by_type, address, "ElastiCache")

    return True


def main():
    state = json.load(sys.stdin)
    by_type = {}
    for resource in managed_resources(state):
        by_type.setdefault(resource["type"], []).append(resource)

    checked = [
        label
        for label, ran in (
            ("S3", check_s3(by_type)),
            ("RDS", check_rds(by_type)),
            ("ElastiCache", check_cache(by_type)),
        )
        if ran
    ]

    if not checked:
        print("\n  No managed substitutions in this example — nothing to assert.")
        return

    print(f"\n  All {', '.join(checked)} substitution assertions passed.")


if __name__ == "__main__":
    main()
