# __SERVICE_NAME__

Generated from a ForgeStack Python service template
(https://github.com/mangopep/forgestack/tree/main/templates/python-service). This project
does not depend on ForgeStack's repository, CI, or configuration in any way — it is a
complete, standalone starting point.

## What's included

- `GET /healthz`, `GET /readyz` — service metadata + readiness (`app/routers/health.py`).
- Request-correlation IDs (`X-Request-Id`), structured JSON logging, and a request-body
  size limit (`app/middleware.py`).
- Typed, environment-validated settings (`app/config.py`).
- Consistent `{"error": "..."}` error responses (`app/errors.py`).
- A hardened Dockerfile (non-root, digest-pinned base image) and CI
  (`.github/workflows/ci.yml`): lint, type check, tests, `pip-audit`, `gitleaks`, and a
  Trivy container scan — self-contained, no reference to any other repository.

## Run it

```bash
pip install -r requirements-lock.txt
uvicorn app.main:app --port __PORT__
curl -i http://localhost:__PORT__/healthz
```

Or containerized:

```bash
docker build -t __SERVICE_NAME__ .
docker run --rm -p __PORT__:__PORT__ __SERVICE_NAME__
curl -i http://localhost:__PORT__/healthz
```

## Add your own routes

Register them in `app/main.py`'s `create_app()` alongside `health.router` — see the
comment there.

## Upgrading from a newer version of this template

This project was generated once; it does not automatically track template changes. See
ForgeStack's `docs/VERSIONING.md` for how template versions are tagged and what a
compatible vs. breaking template change means, if you want to selectively pull in a later
template's improvements by hand.
