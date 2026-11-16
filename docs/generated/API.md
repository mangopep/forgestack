<!-- GENERATED FILE. Do not edit by hand. -->
<!-- Regenerate with: go run ./cmd/docgen generate api -->

# API Reference (generated)

Extracted directly from `internal/platform/router/router.go` by parsing the Go source (`go/ast`) — not hand-written, not AI-generated. If this file is stale relative to the source, regenerate it; the CI docs check fails the build if it's out of sync.

| Method | Path | Observed status codes | Source |
|---|---|---|---|
| GET | `/healthz` | 200 | internal/platform/router/router.go:36 |
| GET | `/readyz` | 200 | internal/platform/router/router.go:43 |
| GET | `/metrics-demo` | 200 | internal/platform/router/router.go:46 |
| GET | `/metrics` | unknown | internal/platform/router/router.go:49 |
| GET | `/api/v1/posts` | unknown | internal/platform/router/router.go:51 |
| POST | `/api/v1/posts` | unknown | internal/platform/router/router.go:52 |

"Observed status codes" lists every status literal passed to `writeJSON` inside that handler's source, in ascending order — it is not a claim about which codes occur in production traffic, only which codes the code is capable of returning.
