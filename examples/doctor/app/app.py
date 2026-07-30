"""composey-doctor: proves real read/write connectivity to managed services.

GET /health runs one check per capability, each conditional on the env vars
composey injects. A capability whose env is absent is "skipped", not failed, so
the same image validates any environment. Returns 200 only if every active check
passes, 503 otherwise — with a per-service breakdown.

Env contract (what composey injects):
  S3    : BUCKET_NAME               (bucket id)
  RDS   : DB_HOST + DB_USERNAME/DB_PASSWORD (secrets)
  RDS   : DATABASE_URL              (secret; credentials and database included)
  Redis : REDIS_URL                 (redis://host:port)
"""

import os

from flask import Flask, jsonify

app = Flask(__name__)
# Stable (non-compact) JSON so callers can match "status": "ok" regardless of env.
app.json.compact = False


def check_s3():
    bucket = os.environ.get("BUCKET_NAME")
    if not bucket:
        return "skipped: BUCKET_NAME not set"
    import boto3

    s3 = boto3.client("s3")
    key = "composey-doctor/health.txt"
    payload = b"composey-doctor"
    s3.put_object(Bucket=bucket, Key=key, Body=payload)
    got = s3.get_object(Bucket=bucket, Key=key)["Body"].read()
    if got != payload:
        raise RuntimeError("S3 round-trip mismatch")
    return f"ok: put+get on bucket {bucket}"


def check_db():
    host = os.environ.get("DB_HOST")
    if not host:
        return "skipped: DB_HOST not set"
    import psycopg2

    conn = psycopg2.connect(
        host=host,
        port=int(os.environ.get("DB_PORT", "5432")),
        user=os.environ["DB_USERNAME"],
        password=os.environ["DB_PASSWORD"],
        dbname=os.environ.get("DB_NAME", "postgres"),
        connect_timeout=5,
    )
    try:
        with conn, conn.cursor() as cur:
            cur.execute(
                "CREATE TABLE IF NOT EXISTS composey_doctor (k text primary key, v text)"
            )
            cur.execute(
                "INSERT INTO composey_doctor (k, v) VALUES ('health', 'ok') "
                "ON CONFLICT (k) DO UPDATE SET v = EXCLUDED.v"
            )
            cur.execute("SELECT v FROM composey_doctor WHERE k = 'health'")
            got = cur.fetchone()[0]
    finally:
        conn.close()
    if got != "ok":
        raise RuntimeError("DB round-trip mismatch")
    return f"ok: write+read on {host}"


def check_redis():
    url = os.environ.get("REDIS_URL")
    if not url:
        return "skipped: REDIS_URL not set"
    import redis

    r = redis.from_url(url, socket_connect_timeout=5)
    r.set("composey-doctor", "ok")
    got = r.get("composey-doctor")
    if got != b"ok":
        raise RuntimeError("Redis round-trip mismatch")
    return f"ok: set+get on {url}"


def check_db_url():
    """
    Connect using only DATABASE_URL, the way most frameworks do.

    This is the check that proves the substitution is complete: the URL has to
    carry credentials the managed instance accepts and name a database that
    exists on it. Nothing here falls back to a separate variable, so a URL
    missing either fails rather than quietly working for another reason.
    """
    url = os.environ.get("DATABASE_URL")
    if not url:
        return "skipped: DATABASE_URL not set"
    import psycopg2

    conn = psycopg2.connect(url, connect_timeout=5)
    try:
        with conn, conn.cursor() as cur:
            cur.execute("SELECT current_database(), current_user")
            database, user = cur.fetchone()
    finally:
        conn.close()
    return f"ok: connected as {user} to {database}"


CHECKS = {
    "s3": check_s3,
    "db": check_db,
    "db_url": check_db_url,
    "redis": check_redis,
}


@app.get("/health")
def health():
    results = {}
    ok = True
    for name, fn in CHECKS.items():
        try:
            results[name] = fn()
        except Exception as e:  # noqa: BLE001 - report any failure verbatim
            results[name] = f"FAIL: {type(e).__name__}: {e}"
            ok = False
    return jsonify({"status": "ok" if ok else "unhealthy", "checks": results}), (
        200 if ok else 503
    )


@app.get("/")
def root():
    # Kept trivially healthy so the ALB target passes while checks run on /health.
    return "composey-doctor: GET /health\n", 200


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=80)
