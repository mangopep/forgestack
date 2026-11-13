# Pinned by digest, not just tag -- re-resolve with:
#   docker pull python:3.12-slim-bookworm && docker inspect --format '{{index .RepoDigests 0}}' python:3.12-slim-bookworm
FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df AS builder
WORKDIR /src
COPY requirements-lock.txt .
RUN pip install --no-cache-dir --no-compile -r requirements-lock.txt

FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df
RUN useradd --create-home --uid 1000 appuser
WORKDIR /app
COPY --from=builder /usr/local/lib/python3.12/site-packages /usr/local/lib/python3.12/site-packages
COPY --from=builder /usr/local/bin /usr/local/bin
COPY app ./app
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    SERVICE_VERSION=docker-build \
    PORT=__PORT__
USER appuser
EXPOSE __PORT__
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD python -c "import os,urllib.request,sys; sys.exit(0 if urllib.request.urlopen(f'http://127.0.0.1:{os.environ[\"PORT\"]}/healthz', timeout=2).status==200 else 1)"
# Shell form so $PORT is actually honored -- see
# https://github.com/mangopep/forgestack/blob/main/docs/adr/0004-f3-application-foundation.md
ENTRYPOINT uvicorn app.main:app --host 0.0.0.0 --port ${PORT}
