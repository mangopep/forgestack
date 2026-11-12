# Posts module architecture

This diagram matches the code as implemented in this milestone (not an aspirational future
state). See `docs/adr/0001-go-kit-for-posts-module.md` for why go-kit was scoped to exactly the
two packages shown here.

```mermaid
flowchart TB
    subgraph composition["internal/platform/router (composition root)"]
        Router["router.New()"]
    end

    subgraph posts["internal/modules/posts"]
        Transport["transport\n(go-kit transport/http)\nencode/decode JSON, status codes"]
        Endpoint["endpoint\n(go-kit endpoint.Endpoint)\nrequest/response structs"]
        Service["service\n(plain Go interface)\nvalidation orchestration, limits"]
        Domain["domain\n(zero dependencies)\nPost, validation rules"]
        Repository["repository\n(plain Go interface)\nIn-memory impl, concurrency-safe"]
    end

    subgraph platform["internal/platform (cross-cutting)"]
        Middleware["middleware\nRequestLogging, MaxBytes"]
        APIError["apierror\nBadRequest marker type"]
    end

    Client["HTTP client"] --> Router
    Router -->|"GET/POST /api/v1/posts"| Middleware
    Middleware --> Transport
    Router -->|"GET /healthz, /readyz, /metrics-demo\n(inline handlers, no go-kit)"| Client

    Transport --> Endpoint
    Endpoint --> Service
    Service --> Domain
    Service --> Repository

    Transport -.->|"classify errors via"| APIError
    Endpoint -.->|"classify errors via"| APIError

    classDef gokit fill:#2b6cb0,color:#fff,stroke:#1a4971
    class Transport,Endpoint gokit
```

**What the blue boxes mean**: `transport` and `endpoint` are the only packages in this module
that import go-kit. `domain` and `service` import nothing from go-kit and nothing from
`net/http` — they can be, and are, unit-tested with no HTTP server and no go-kit types
involved (see `internal/modules/posts/service/service_test.go`, which uses a fake repository).

**Why this shape**: if go-kit needs to be replaced later (see the ADR's maintenance-risk
discussion), only `transport` and `endpoint` need rewriting. `domain`, `service`, and
`repository` are unaffected.
