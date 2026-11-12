---
title: Application and deployment architecture
---

# Application and deployment architecture

**Status: implemented** for the request flow and local Docker build; **not verified** for
anything beyond a local `docker compose` run (no deployed environment exists yet — that's
F9/F7 territory).

## Current application architecture (go-kit layering, ADR 0001)

```mermaid
flowchart LR
    req["HTTP request"] --> router["internal/platform/router<br/>(composition root)"]
    router --> mw["internal/platform/middleware"]
    mw --> transport["modules/posts/transport<br/>(HTTP decode/encode)"]
    transport --> endpoint["modules/posts/endpoint<br/>(go-kit endpoint)"]
    endpoint --> service["modules/posts/service<br/>(business logic)"]
    service --> repo["modules/posts/repository<br/>(in-memory, concurrency-safe)"]
    service --> domain["modules/posts/domain<br/>(validation, invariants)"]
    endpoint -.->|structured errors| apierror["internal/platform/apierror"]

    classDef layer fill:#2c5282,color:#fff,stroke:#2a4365
    class router,mw,transport,endpoint,service,repo,domain,apierror layer
```

Each arrow is a real Go import, not an aspirational boundary — `service` has no dependency on
`transport` (business logic doesn't know about HTTP), and `repository` is swappable behind the
same interface the service depends on. See ADR 0001 for the full tradeoff discussion.

## Current Docker / local-deployment architecture

```mermaid
flowchart TB
    subgraph build["Multi-stage build (Dockerfile)"]
        builder["golang:1.27.4-alpine<br/>CGO_ENABLED=0, static binary"]
        runtime["gcr.io/distroless/static-debian12:nonroot<br/>no shell, no package manager"]
        builder -->|binary only| runtime
    end

    subgraph compose["docker compose (local only)"]
        api["api service<br/>read_only, no-new-privileges,<br/>127.0.0.1-bound port"]
        pg["postgres (profile: data)"]
        redis["redis (profile: data)"]
        ch["clickhouse (profile: analytics)"]
    end

    runtime --> api
    api -.->|optional, profile-gated| pg
    api -.->|optional, profile-gated| redis
    api -.->|optional, profile-gated| ch

    classDef prod fill:#2f855a,color:#fff,stroke:#22543d
    classDef optional fill:#718096,color:#fff,stroke:#4a5568,stroke-dasharray: 4 3
    class builder,runtime,api prod
    class pg,redis,ch optional
```

**Not verified beyond local `docker compose up`**: no Kubernetes deployment, no cloud-hosted
instance, no CI job that deploys this image anywhere. `container-scan.yml` builds and scans
the image on every PR (Trivy, HIGH/CRITICAL) but never pushes it to a registry — see
`docs/handbook/RELEASE-WORKFLOW.md` for the planned (not built) build-once/promote-by-digest
pipeline this would need before any real deployment.
