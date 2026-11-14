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
# applied to ForgeStack's own service (98 HIGH/CRITICAL -> 0).
FROM python:3.12-alpine@sha256:c5ba3deef00c7758561a0f03c26f44f10589263d40e732b8748e0d1f38f6dd89 AS builder
WORKDIR /src
COPY requirements-lock.txt .
RUN pip install --no-cache-dir --no-compile -r requirements-lock.txt

FROM python:3.12-alpine@sha256:c5ba3deef00c7758561a0f03c26f44f10589263d40e732b8748e0d1f38f6dd89
# Patches the two OS packages with an available point-release fix at
# assessment time; re-resolve with `apk list -I <pkg>` inside this image
# if this ever needs bumping.
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
