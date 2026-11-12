---
title: Observability
---

# Observability

**Status: Planned, not yet implemented.**

Structured application logging, correlation/request IDs, OpenTelemetry instrumentation,
Prometheus metrics, Grafana dashboards, Loki logs, Tempo traces, SLOs, and alerting rules are
roadmap module 17 (`docs/ROADMAP.md`) and phase F8 of the engineering plan — none of it exists
in this repository yet. The Go API currently logs to stdout with no structured format beyond
what the standard library provides, and `compose.yaml`'s optional services (`postgres`,
`redis`, `clickhouse`) have no monitoring wired to them.

This page exists so the documentation site's navigation matches the intended information
architecture without a broken or missing link — not to describe a capability that doesn't
exist. When F8 lands, this page becomes real content, not a redirect.
