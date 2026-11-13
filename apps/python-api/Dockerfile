# Multi-stage build: a build stage with pip, a minimal runtime stage
# without it. python:3.12-slim-bookworm is pinned (not 3.14, which matches
# some developers' local Python) deliberately -- it is the widely-available,
# well-tested image tag; the service itself only requires Python >=3.11
# (see pyproject.toml), so this is a safe, portable choice, not a
# compatibility gap.
# Pinned by digest, not just tag (F4 supply-chain hardening) -- see
# docs/adr/0005-f4-security-supply-chain.md for the update procedure.
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
# VERSION defaults to "docker-build" -- the honest default for an ordinary
# (non-release) build. release.yml's publish job passes
# --build-arg VERSION=<tag> so a published image's /healthz actually
# reports the real release version -- this hardcoded value was the same
# class of bug the Go Dockerfile had (see its own comment above); found
# together while investigating v0.1.0's failed release attempt.
ARG VERSION=docker-build
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    SERVICE_VERSION=${VERSION} \
    PORT=8081
USER appuser
EXPOSE 8081
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD python -c "import os,urllib.request,sys; sys.exit(0 if urllib.request.urlopen(f'http://127.0.0.1:{os.environ[\"PORT\"]}/healthz', timeout=2).status==200 else 1)"
# Shell form so $PORT is actually honored (a bare exec-form ENTRYPOINT
# cannot expand environment variables) -- config.py's `port` setting would
# otherwise be validated but never actually used to bind the server, which
# was a real bug caught and fixed during F3 hardening.
ENTRYPOINT uvicorn app.main:app --host 0.0.0.0 --port ${PORT}
