---
title: F3 application foundation (Go + Python)
---

# F3 application foundation: two independent reference services

**Status: implemented.** Both services exist, run, and are tested; see
`docs/platform/CONTRACTS.md` for the full contract specification this diagram
summarizes, and `docs/adr/0004-f3-application-foundation.md` for the decisions behind it.

```mermaid
flowchart TB
    subgraph goapi["Go reference service (cmd/api)"]
        direction TB
        goRouter["internal/platform/router<br/>(composition root)"]
        goMw["middleware: CorrelationID, RequestLogging, MaxBytes"]
        goPosts["modules/posts (go-kit layered:<br/>transport/endpoint/service/repository/domain)"]
        goRouter --> goMw --> goPosts
    end

    subgraph pyapi["Python reference service (apps/python-api)"]
        direction TB
        pyApp["app.main (FastAPI app factory + lifespan)"]
        pyMw["middleware: CorrelationID, RequestLogging, MaxBodySize"]
        pyItems["routers/items + schemas.py (Pydantic) + store.py"]
        pyApp --> pyMw --> pyItems
    end

    subgraph contract["Shared contract (docs/platform/CONTRACTS.md) -- independently implemented, not a shared library"]
        direction LR
        c1["GET /healthz, /readyz"]
        c2["{'error': msg} on every failure"]
        c3["X-Request-Id, reused or generated"]
        c4["JSON logs: method/path/status/duration_ms/request_id"]
        c5["graceful shutdown on SIGINT/SIGTERM"]
    end

    goapi -.->|implements| contract
    pyapi -.->|implements| contract

    subgraph compose["docker compose (local only)"]
        goContainer["api service :8080"]
        pyContainer["python-api service :8081"]
    end
    goapi --> goContainer
    pyapi --> pyContainer

    classDef impl fill:#2f855a,color:#fff,stroke:#22543d
    class goapi,pyapi,goContainer,pyContainer impl
```

## Reading this diagram

- The two services share **zero runtime code** — no shared library, no shared base
  image, no shared process. What they share is a documented convention, implemented
  independently in each language's own idiom. This is a deliberate choice (see ADR 0004
  and `CONTRACTS.md`'s "why independent implementations" section): a shared library would
  itself become a dependency every *future* language joining this contract (Rust,
  TypeScript, an AI workload) would need to adopt, which is a heavier commitment than
  documenting a shape each language implements on its own.
- Both services run independently (`docker compose up python-api` or `up api` alone)
  and together (`docker compose up api python-api`) — neither depends on the other.
- Neither service has a real external dependency yet (both use in-memory storage), which
  is why `/readyz` and `/healthz` are equivalent in practice today — see `CONTRACTS.md`
  §2 for why the distinction still exists.

## What this diagram does not show (by design, not omission)

No database, message broker, Kubernetes, or observability backend — none exist yet,
and showing them here would misrepresent current state. See
`docs/architecture/INFRASTRUCTURE-OWNERSHIP.md` for what's planned there, clearly
labeled as not yet built.
