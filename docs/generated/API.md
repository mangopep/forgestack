<!-- GENERATED FILE. Do not edit by hand. -->
<!-- Regenerate with: go run ./cmd/docgen generate api -->

# API Reference (generated)

Extracted directly from `internal/api/api.go` by parsing the Go source (`go/ast`) — not hand-written, not AI-generated. If this file is stale relative to the source, regenerate it; the CI docs check fails the build if it's out of sync.

| Method | Path | Observed status codes | Source |
|---|---|---|---|
| GET | `/healthz` | 200 | internal/api/api.go:57 |
| GET | `/readyz` | 200 | internal/api/api.go:60 |
| GET | `/api/v1/posts` | 200, 400 | internal/api/api.go:63 |
| POST | `/api/v1/posts` | 201, 400 | internal/api/api.go:75 |
| GET | `/metrics-demo` | 200 | internal/api/api.go:100 |

"Observed status codes" lists every status literal passed to `writeJSON` inside that handler's source, in ascending order — it is not a claim about which codes occur in production traffic, only which codes the code is capable of returning.
