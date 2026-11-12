# Go engineering standards
- Prefer Go standard library first; justify dependencies before adding.
- Use `context.Context` through request and database boundaries, proper timeouts and graceful shutdown.
- Bound input sizes, pagination and concurrency; avoid unbounded goroutines and allocations.
- Keep business logic independent of HTTP/database glue; return structured errors without sensitive internals.
- Test normal, edge, concurrency, auth-negative and failure paths. Use race detector where available.
- Record API contracts and metrics. Measure before optimizing; profile p95/p99 latency and allocations.
- Run `gofmt`, `go vet`, `go test ./...`, `go test -race ./...` when possible.
