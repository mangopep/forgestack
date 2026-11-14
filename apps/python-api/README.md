# ForgeStack Python reference service

FastAPI, typed Pydantic schemas, the same operational contracts as the Go API
(`cmd/api`) — see `docs/platform/CONTRACTS.md` for exactly what's shared and what's
deliberately language-idiomatic instead.

## Requirements

Python >= 3.11 (local dev). The Docker image pins `python:3.12-slim-trixie`
regardless of your local interpreter version.

## Install

```bash
cd apps/python-api
python3 -m venv .venv
source .venv/bin/activate
pip install -e ".[dev]"
```

`[dev]` includes `pytest`, `httpx` (for `TestClient`), `ruff`, `mypy` — omit it for a
production-only install (`pip install -e .`, or `pip install -r requirements-lock.txt`
for exact pinned versions including transitive dependencies).

## Run locally

```bash
uvicorn app.main:app --reload --port 8081
```

Then: `curl http://127.0.0.1:8081/healthz`, interactive API docs at
`http://127.0.0.1:8081/docs`.

## Test

```bash
python -m pytest -v
```

## Lint / format / type-check

```bash
ruff check .
ruff format .
mypy app
```

## Docker

```bash
docker build -t forgestack-python-api .
docker run --rm -p 8081:8081 forgestack-python-api
```

Or via the repo-root Compose file: `docker compose up --build python-api` (runs
alongside the Go API; see the root `compose.yaml`).

## Configuration

All environment variables are validated at startup (`app/config.py`, via
`pydantic-settings`) — see `docs/generated/CONFIG.md` for the generated reference
covering both services, or `docs/platform/CONTRACTS.md` §1 for the convention.

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8081` | Also controls the Docker image's `uvicorn --port` (see the Dockerfile) |
| `SERVICE_NAME` | `forgestack-python-api` | Reported in `/healthz` |
| `SERVICE_VERSION` | `dev` | Reported in `/healthz`; set to a real value at build time |
| `LOG_LEVEL` | `INFO` | Python `logging` level name |
| `REQUEST_BODY_MAX_BYTES` | `4096` | Matches the Go API's `middleware.MaxBytes` default |

## API

- `GET /healthz`, `GET /readyz` — see `docs/platform/CONTRACTS.md` §2.
- `GET /api/v1/items?limit=N`, `POST /api/v1/items` — example typed resource.
- `GET /openapi.json`, `GET /docs` — auto-generated OpenAPI contract (FastAPI).

## Known limitations

See `docs/platform/CONTRACTS.md`'s "Known limitations" section — no auth, no real
datastore, no distributed tracing yet; all explicitly out of scope for this phase.
