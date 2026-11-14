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

## If a security check fails

`ci.yml`'s `security` job (`pip-audit`) or `container` job (Trivy) failing means a real,
currently-known vulnerability was found — not a flake to retry past:

- **`pip-audit` fails**: a dependency in `requirements-lock.txt` has a known CVE. Check if
  a newer version fixes it (`pip index versions <package>`), bump it in
  `requirements-lock.txt`, and re-run the full test suite before trusting the bump.
- **Trivy (container scan) fails**: either an OS package in the base image or a Python
  dependency has a known CVE. Re-run locally with no severity filter to see everything,
  not just what's gating the build: `trivy image --no-progress <image>:latest`. If it's
  the base image and a newer point release of the same Python/OS combination fixes it,
  re-resolve the digest (see the `Dockerfile`'s own comment) and rebuild. If it's a
  specific OS package with its own point-release fix available, pin an `apk upgrade
  <package>=<version>` (Alpine) in the Dockerfile rather than waiting for a full base-image
  bump — see
  https://github.com/mangopep/forgestack/blob/main/apps/python-api/Dockerfile for a worked
  example of exactly this.
- **Never** add a blanket `ignore-unfixed` or raise the severity threshold just to make a
  red check green. If a finding genuinely has no fix yet, that's a real, documented
  exception with an owner and a review date — not something to silently suppress.

## Upgrading from a newer version of this template

This project was generated once; it does not automatically track template changes. See
ForgeStack's `docs/VERSIONING.md` for how template versions are tagged and what a
compatible vs. breaking template change means, if you want to selectively pull in a later
template's improvements by hand.
