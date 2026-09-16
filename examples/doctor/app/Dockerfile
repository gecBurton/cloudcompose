FROM python:3.12-slim
# psycopg2-binary and redis are pure-wheel installs; no system build deps needed.
# azure-storage-blob/azure-identity and google-cloud-storage are only ever
# imported by check_s3 on their respective clouds (see its own comment) --
# installed unconditionally since the same image runs on all three clouds.
RUN pip install --no-cache-dir flask boto3 psycopg2-binary redis \
    azure-storage-blob azure-identity google-cloud-storage
COPY app.py /app.py
CMD ["python", "/app.py"]
