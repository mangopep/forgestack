# Multi-stage build: a build stage with pip, a minimal runtime stage
# without it. python:3.12-alpine (not slim-bookworm/-trixie, not 3.14,
# which matches some developers' local Python) -- the service only
# requires Python >=3.11 (see pyproject.toml). Moved bookworm -> trixie
# -> alpine across issue #40's investigation, each step verified before
# switching, not assumed:
#   1. bookworm -> trixie (same Debian "slim" family): 98 HIGH/CRITICAL
#      findings (2 critical) -> 47 (0 critical).
#   2. trixie -> alpine/musl: re-tested after F3 originally rejected
#      Alpine/musl for this image (no prebuilt wheels existed then for
#      pydantic_core/uvloop/httptools/watchfiles, all Rust/C extensions).
#      Re-verified 2026-11-14: musllinux wheels now exist for all four on
#      PyPI -- `pip install -r requirements-lock.txt` installs cleanly on
#      python:3.12-alpine with NO compiler needed, full test suite (11/11)
#      passes, the service runs and /healthz responds. Unfiltered Trivy:
#      47 HIGH (trixie) -> 0 HIGH/CRITICAL (alpine, after the krb5-libs/
#      zlib apk upgrades below) -- only 5 MEDIUM/1 LOW remain, from pip's
#      own package metadata still present in the copied site-packages
#      (not a runtime dependency; a candidate follow-up, not blocking).
#      Image also shrank: 384MB (trixie) -> 159MB (alpine).
# Pinned by digest, not just tag (F4 supply-chain hardening) -- see
# docs/adr/0005-f4-security-supply-chain.md for the update procedure.
FROM python:3.12-alpine@sha256:c5ba3deef00c7758561a0f03c26f44f10589263d40e732b8748e0d1f38f6dd89 AS builder
WORKDIR /src
COPY requirements-lock.txt .
RUN pip install --no-cache-dir --no-compile -r requirements-lock.txt

FROM python:3.12-alpine@sha256:c5ba3deef00c7758561a0f03c26f44f10589263d40e732b8748e0d1f38f6dd89
# Patches the two OS packages (krb5-libs, zlib) that had an available
# point-release fix at assessment time -- this cleared every remaining
# HIGH/CRITICAL finding (16 HIGH -> 0). Version-pinned to match this
# repo's own supply-chain pinning convention.
# UNLIKE the base image's digest pin above, this is NOT a long-term-stable
# pin: Alpine's apk repos only ever serve the CURRENT version of a package
# for a given Alpine release (confirmed: `apk add pkg=<older-version>`
# fails outright, "unable to select packages" -- there is no snapshot
# archive of older versions the way Debian has). Once Alpine ships a
# newer krb5-libs/zlib, THIS EXACT PIN WILL FAIL THE BUILD (not silently
# drift) with that same error. When that happens: re-resolve with
# `apk update && apk search <pkg>` inside a fresh python:3.12-alpine
# container and bump the version here -- same maintenance event as
# re-resolving the base image digest, just on apk's own cadence instead.
RUN apk add --no-cache --upgrade krb5-libs=1.22.3-r0 zlib=1.4-r0 \
    && adduser -D -u 1000 appuser
WORKDIR /app
COPY --from=builder /usr/local/lib/python3.12/site-packages /usr/local/lib/python3.12/site-packages
# Only the one console-script binary actually needed at runtime -- the
# builder's /usr/local/bin also carries pip, mypy, ruff, pytest, idle,
# and other dev-only tooling that has no reason to ship in the running
# container (smaller image, smaller attack surface, matches "remove
# unnecessary dependencies" -- issue #40).
COPY --from=builder /usr/local/bin/uvicorn /usr/local/bin/uvicorn
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
