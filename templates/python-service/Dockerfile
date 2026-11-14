# Pinned by digest, not just tag -- re-resolve with:
#   docker pull python:3.12-alpine && docker inspect --format '{{index .RepoDigests 0}}' python:3.12-alpine
# python:3.12-alpine (not slim-bookworm/-trixie): re-tested 2026-11-14
# after F3 originally rejected Alpine/musl for this service (no prebuilt
# wheels existed then for pydantic_core/uvloop/httptools/watchfiles, all
# Rust/C extensions). musllinux wheels now exist for all four on PyPI --
# verified compatible (generated project's own pytest suite, a real
# build/run, /healthz) before switching, not assumed; see
# https://github.com/mangopep/forgestack/blob/main/apps/python-api/Dockerfile
# and issue #40 for the full bookworm -> trixie -> alpine investigation
# applied to ForgeStack's own service (98 HIGH/CRITICAL -> 0 findings of
# any severity, after also removing pip's own package metadata below).
FROM python:3.12-alpine@sha256:c5ba3deef00c7758561a0f03c26f44f10589263d40e732b8748e0d1f38f6dd89 AS builder
WORKDIR /src
COPY requirements-lock.txt .
RUN pip install --no-cache-dir --no-compile -r requirements-lock.txt

FROM python:3.12-alpine@sha256:c5ba3deef00c7758561a0f03c26f44f10589263d40e732b8748e0d1f38f6dd89
# Patches the two OS packages with an available point-release fix at
# assessment time. UNLIKE the base image's digest pin above, this is NOT
# a long-term-stable pin: Alpine's apk repos only ever serve the CURRENT
# version of a package for a given Alpine release (confirmed: `apk add
# pkg=<older-version>` fails outright, "unable to select packages" -- no
# snapshot archive the way Debian has). Once Alpine ships a newer
# krb5-libs/zlib, THIS EXACT PIN WILL FAIL THE BUILD (not silently
# drift). When that happens: re-resolve with `apk update && apk search
# <pkg>` inside a fresh python:3.12-alpine container and bump the
# version here.
RUN apk add --no-cache --upgrade krb5-libs=1.22.3-r0 zlib=1.4-r0 \
    && adduser -D -u 1000 appuser
WORKDIR /app
COPY --from=builder /usr/local/lib/python3.12/site-packages /usr/local/lib/python3.12/site-packages
# Only the one console-script binary actually needed at runtime -- the
# builder's /usr/local/bin also carries pip, mypy, ruff, pytest, and
# other dev-only tooling that has no reason to ship in the running
# container.
COPY --from=builder /usr/local/bin/uvicorn /usr/local/bin/uvicorn
COPY app ./app
# pip ships baked into python:3.12-alpine's own site-packages and the
# builder-stage copy above carries it forward too -- it is not a
# runtime dependency of this service. Removed here, after the copy
# (removing it before would just have it reintroduced, since the
# builder's own site-packages has it too). This clears the last
# handful of MEDIUM/LOW findings (all pip's own CVEs) down to zero.
RUN rm -rf /usr/local/lib/python3.12/site-packages/pip \
           /usr/local/lib/python3.12/site-packages/pip-*.dist-info
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
