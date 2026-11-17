---
title: Observability
---

# Observability

**Status: local-development foundation implemented and verified; production-grade monitoring
is not implemented.** This page previously said "Planned, not yet implemented — none of it
exists in this repository yet," which stopped being true once F6 landed. Corrected here
rather than left stale.

## What's implemented and locally verified

- **Structured logs**: `log/slog` JSON logs from the Go API, correlated via `request_id`
  (`internal/platform/middleware.CorrelationID`/`RequestLogging`) and `trace_id` once a span
  exists.
- **OpenTelemetry tracing**: `internal/platform/telemetry.Setup` creates and samples spans
  with zero collector required (nothing exported until `OTEL_EXPORTER_OTLP_ENDPOINT` is set);
  W3C `traceparent`/`baggage` propagation is handled explicitly, not via a global that might
  not be configured yet.
- **Prometheus metrics**: real `http_requests_total`/`http_request_errors_total`/
  `http_request_duration_seconds` collectors served at `GET /metrics`, standard Prometheus
  text exposition format.
- **A local observability stack** (`observability/compose.yaml`): Prometheus, Loki, Tempo,
  Grafana, an OpenTelemetry Collector, and Promtail — independently attachable, never
  required to run a service. Every published port binds to `127.0.0.1` only. Grafana runs
  with anonymous Admin access and no login form, which is survivable *because* of that
  loopback binding — see `observability/README.md`'s "Security posture" section for the full
  local-dev-vs-production distinction, which this page defers to rather than duplicating.
- All of the above is mirrored into `templates/go-service`, so every generated service gets
  the same instrumentation, not just the main API.

**Verified how:** built and ran a generated service through the real Docker daemon, pointed
it at the stack, and confirmed by direct API query (not just that containers started) that
Prometheus scraped real metrics, Loki received logs with `trace_id`, Tempo held the matching
trace, and Grafana's provisioned dashboard resolves against real data. Full detail and
exact commands in `observability/README.md`.

## What's explicitly not implemented (production-grade monitoring)

None of the following exist, and nothing here should be mistaken for them:

- Grafana real authentication/RBAC, Loki authenticated ingestion with retention/ingestion
  limits, Tempo retention/sampling policy, durable/HA Prometheus, recording rules, or
  Alertmanager routing and tested notification delivery.
- Any cloud-managed backend (CloudWatch, X-Ray, Grafana Cloud, etc.) — everything above runs
  on local Docker only.
- SLOs, alerting rules, or realistic-load/failure/recovery testing against this
  instrumentation.
- Observability for anything beyond the Go API: no database, AI/RAG/agent, MCP server, queue,
  or Kubernetes workload exists in this repository yet to instrument.

## Next approval gate

Production-grade monitoring profile (real auth throughout, retention/HA policy matched to
actual data sensitivity, alerting) is its own increment, tracked as F8 in `docs/ROADMAP.md`,
and requires either a real deployment target or an explicit decision to build it against the
local stack anyway with documented limitations — not assumed as a natural next step of F6.
