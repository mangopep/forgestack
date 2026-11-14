# Multi-stage build: a build stage with pip, a minimal runtime stage
# without it. python:3.12-slim-trixie is pinned (not 3.14, which matches
# some developers' local Python) deliberately -- it is the widely-available,
# well-tested image tag; the service itself only requires Python >=3.11
# (see pyproject.toml), so this is a safe, portable choice, not a
# compatibility gap. Moved from bookworm to trixie (2026-11-14, issue #40):
# a newer Debian release with a smaller set of outstanding OS-package CVEs
# in the same official, glibc-based "slim" family -- verified compatible
# (full test suite, ruff, mypy, a real build/run, and /healthz all still
# pass unchanged) before switching, not assumed.
# Pinned by digest, not just tag (F4 supply-chain hardening) -- see
# docs/adr/0005-f4-security-supply-chain.md for the update procedure.
FROM python:3.12-slim-trixie@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b AS builder
WORKDIR /src
COPY requirements-lock.txt .
RUN pip install --no-cache-dir --no-compile -r requirements-lock.txt

FROM python:3.12-slim-trixie@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
# Patches the one OS package (zlib1g) that had an available point-release
# fix within trixie itself at assessment time (issue #40) -- everything
# else in the base image's remaining findings has no upstream fix yet.
# Version pinned (not a bare `--only-upgrade`) to match this repo's own
# supply-chain pinning convention and hadolint's DL3008 check; re-resolve
# with `apt-cache policy zlib1g` inside this image if this ever needs
# bumping again. Removes apt's own cache afterward so it isn't shipped in
# the final layer.
RUN apt-get update && apt-get install --yes --no-install-recommends \
    --only-upgrade zlib1g=1:1.3.dfsg+really1.3.1-1+deb13u1 \
    && rm -rf /var/lib/apt/lists/*
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
