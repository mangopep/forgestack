---
title: Platform overview
---

# Platform overview

**Status: implemented** for the Go application layer, F1–F5 governance/security/templates,
and the F6 local module resolver and playground lifecycle. Cloud infrastructure lifecycle,
AI modules and production observability remain planned; this page does not claim those
capabilities exist.

## Language-independent boundaries

```mermaid
flowchart TB
    subgraph impl["Implemented today"]
        direction TB
        Go["Go API (cmd/api, internal/modules/posts)"]
        Gov["Branch governance + CI (internal/branchpolicy, .github/workflows)"]
        Docs["Documentation-as-code (internal/docgen, website/)"]
        Compose["Module manifests + compatibility resolver (cmd/modulecheck)"]
        Playground["Local playground ownership + verified scoped cleanup"]
    end
    subgraph planned["Planned, not yet built"]
        direction TB
        Py["Python / FastAPI service (F12)"]
        Node["Node.js / TypeScript service"]
        Workers["Background workers / event-driven (F11)"]
    end
    subgraph shared["Shared platform contracts (language-independent by design)"]
        direction TB
        CI["CI quality/security gates (ci.yml, codeql.yml, secret-scan.yml)"]
        Infra["Terraform modules (infrastructure/)"]
        Obs["Observability contracts (F8, planned)"]
    end

    Go --> shared
    Compose --> Go
    Playground --> Compose
    Py -.-> shared
    Node -.-> shared
    Workers -.-> shared

    classDef done fill:#2f855a,color:#fff,stroke:#22543d
    classDef todo fill:#718096,color:#fff,stroke:#4a5568,stroke-dasharray: 4 3
    class Go,Gov,Docs,Compose,Playground done
    class Py,Node,Workers,Obs todo
```

The point of this boundary: `ci.yml`'s quality/security gates, the Terraform module
conventions, and the branch-promotion policy are all designed to apply to *any* service added
later, regardless of language — nothing in `internal/branchpolicy` or the required-checks list
is Go-specific. A future Python or Node service would plug into the same `feature/* → develop →
staging → main` flow and the same required-checks shape, not a parallel one.

## Repository separation of concerns

```mermaid
flowchart LR
    cmd["cmd/ — entrypoints only<br/>(api, docgen, branchpolicy)"]
    internal["internal/ — all business logic,<br/>never imported by other repos"]
    infra["infrastructure/ — Terraform,<br/>opt-in, not wired to the API"]
    docs["docs/ — source of truth;<br/>website/ renders it, never copies it"]
    gh[".github/ — CI, governance,<br/>templates"]
    tests["tests/ — cross-package<br/>integration tests"]

    cmd --> internal
    tests --> internal
    docs -.->|rendered by| website["website/ (Docusaurus)"]
    gh -->|enforces promotion for| internal
    gh -->|enforces promotion for| docs

    classDef root fill:#2c5282,color:#fff,stroke:#2a4365
    class cmd,internal,infra,docs,gh,tests,website root
```

Each root folder has exactly one responsibility (per the repo's engineering rules): `cmd/`
never contains logic beyond wiring, `internal/` is never imported outside this repository
(Go's own `internal/` visibility rule enforces this), and `website/` never holds hand-written
content — it is purely a renderer over `docs/`.
