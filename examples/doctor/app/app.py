"""cloudcompose-doctor: proves real read/write connectivity to managed services.

GET /health runs one check per capability, each conditional on the env vars
cloudcompose injects. A capability whose env is absent is "skipped", not failed, so
the same image validates any environment. Returns 200 only if every active check
passes, 503 otherwise — with a per-service breakdown.

Env contract (what cloudcompose injects):
  S3/Blob/GCS : BUCKET_NAME + BLOBS_URL   (bucket/account id + endpoint;
                BLOBS_URL's scheme/host picks which object-storage SDK to
                use -- see check_s3's own comment)
  RDS   : DB_HOST + DB_USERNAME/DB_PASSWORD (secrets)
  RDS   : DATABASE_URL              (secret; credentials and database included)
  Redis : REDIS_URL                 (redis://host:port, or rediss:// on Azure)
"""

import os

from flask import Flask, jsonify

app = Flask(__name__)
# Stable (non-compact) JSON so callers can match "status": "ok" regardless of env.
app.json.compact = False


def check_s3():
    """Round-trips a small object through whichever object-storage service
    cloudcompose substituted minio for.

    BUCKET_NAME alone doesn't say which cloud's SDK to use: AWS, Azure, and
    GCP all inject it as the bucket/account identifier. BLOBS_URL (the
    endpoint) disambiguates by scheme/host -- AWS never sets it (an app
    talking to S3 doesn't need an explicit endpoint), Azure's is a
    *.blob.core.windows.net URL, and GCP's is gs://.
    """
    bucket = os.environ.get("BUCKET_NAME")
    if not bucket:
        return "skipped: BUCKET_NAME not set"

    blobs_url = os.environ.get("BLOBS_URL", "")
    key = "cloudcompose-doctor/health.txt"
    payload = b"cloudcompose-doctor"

    if blobs_url.startswith("gs://"):
        from google.cloud import storage

        blob = storage.Client().bucket(bucket).blob(key)
        blob.upload_from_string(payload)
        got = blob.download_as_bytes()
        if got != payload:
            raise RuntimeError("GCS round-trip mismatch")
        return f"ok: put+get on GCS bucket {bucket}"

    if ".blob.core.windows.net" in blobs_url:
        from azure.identity import DefaultAzureCredential
        from azure.storage.blob import BlobServiceClient

        # The container is always named after the compose service doctor's
        # own compose.yml declares it as ("blobs") -- the same name
        # inferStorageAzure gives the azurerm_storage_container it creates
        # for that service, deterministically, not something this app
        # discovers at runtime.
        client = BlobServiceClient(account_url=blobs_url, credential=DefaultAzureCredential())
        blob_client = client.get_blob_client(container="blobs", blob=key)
        blob_client.upload_blob(payload, overwrite=True)
        got = blob_client.download_blob().readall()
        if got != payload:
            raise RuntimeError("Azure Blob round-trip mismatch")
        return f"ok: put+get on Azure Blob container 'blobs' (account {bucket})"

    import boto3

    s3 = boto3.client("s3")
    s3.put_object(Bucket=bucket, Key=key, Body=payload)
    got = s3.get_object(Bucket=bucket, Key=key)["Body"].read()
    if got != payload:
        raise RuntimeError("S3 round-trip mismatch")
    return f"ok: put+get on S3 bucket {bucket}"


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
                "CREATE TABLE IF NOT EXISTS cloudcompose_doctor (k text primary key, v text)"
            )
            cur.execute(
                "INSERT INTO cloudcompose_doctor (k, v) VALUES ('health', 'ok') "
                "ON CONFLICT (k) DO UPDATE SET v = EXCLUDED.v"
            )
            cur.execute("SELECT v FROM cloudcompose_doctor WHERE k = 'health'")
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
    r.set("cloudcompose-doctor", "ok")
    got = r.get("cloudcompose-doctor")
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
    return "cloudcompose-doctor: GET /health\n", 200


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=80)
