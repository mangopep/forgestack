# ForgeStack local observability stack

## What this is

A docker-compose stack of five pieces, each doing one job:

- **Prometheus** — scrapes a service's `/metrics` endpoint on a timer and stores the
  numbers (requests, errors, latency) so you can query them over time.
- **Loki** — the same idea as Prometheus, but for log lines instead of numbers.
- **Tempo** — stores distributed traces (the span data `internal/platform/telemetry`
  creates for every request).
- **Grafana** — the dashboard/explore UI on top of all three.
- **OpenTelemetry Collector** — a small relay: your service sends traces to it over
  OTLP, it forwards them to Tempo. (Metrics and logs skip the collector entirely —
  Prometheus and Promtail read them directly — so there's exactly one path per signal.)
- **Promtail** — reads a container's stdout/stderr and ships it to Loki.

## Why it's separate from the main `compose.yaml`

A ForgeStack service (the main API or anything `scripts/new-service.sh` generates)
**never requires this stack to run.** `internal/platform/telemetry.Setup` creates spans
and serves `/metrics` either way; with no `OTEL_EXPORTER_OTLP_ENDPOINT` set, spans are
just dropped instead of exported, with zero errors or retries. This stack is what you
start when you want to **look at** that telemetry, not a dependency of the service
itself — "do not force a simple API to launch the entire monitoring stack."

## When to use it

- You want to see real request-rate/error-rate/latency graphs instead of reading
  `/metrics`' raw text.
- You want to see one request's trace (how long each part took) instead of just a
  duration number.
- You're teaching/learning what Prometheus, Loki, Tempo, and Grafana actually do by
  running them, not just reading about them.

## Alternatives and trade-offs

- **AWS CloudWatch** (metrics+logs) / **AWS X-Ray** (traces) are the managed-cloud
  equivalent of this stack — real money, real AWS account, out of scope here (F6
  explicitly does not provision AWS). `internal/platform/telemetry`'s OTLP exporter and
  Prometheus endpoint are vendor-neutral, so pointing them at a managed backend later is
  a configuration change, not a code change.
- **Grafana Alloy** could replace both the OpenTelemetry Collector and Promtail with one
  binary. Kept as two separate, more widely documented pieces here for a first pass;
  Alloy is a reasonable follow-up once you're comfortable with what each piece does.
- **Skipping Promtail** and reading `docker logs` directly is fine for a single
  container; Promtail only starts paying for itself once logs come from more than one
  service.

## How to run it

```bash
docker compose -f observability/compose.yaml up -d
```

**What each new flag means:** `-f observability/compose.yaml` points at this stack's
own compose file instead of the repo-root one (they are independent — nothing here
touches the Postgres/Redis/ClickHouse sandbox in the root `compose.yaml`). `-d` runs it
in the background ("detached") instead of attaching your terminal to every container's
logs.

**Expected output:** six lines, each ending `Started` (`otel-collector`, `tempo`,
`prometheus`, `loki`, `promtail`, `grafana`).

**What might fail:**
- Port already in use (3000, 3100, 3200, 4317, 4318, or 9090) — something else on your
  Mac is using it; stop that first or edit the port mapping in `compose.yaml`.
- Docker Desktop not running — `docker compose` will say it can't connect to the daemon;
  start Docker Desktop and retry.

**Cost impact:** none. Every image is free and runs entirely on your machine; nothing
here talks to AWS or any paid API.

### Point a running service at it

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
go run ./cmd/api              # or a generated service's ./cmd/api
```

If the service is running as its own container instead (so Promtail can pick up its
logs), add the label Promtail's filter looks for and point it at the collector over the
shared Docker network:

```bash
docker run -d --name my-service \
  --network forgestack-observability_default \
  --label com.forgestack.logs=true \
  --label com.forgestack.service=my-service \
  -e OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318 \
  -p 8080:8080 my-service:test
```

Then send it some traffic (`curl localhost:8080/healthz` a few times) and open
<http://localhost:3000> — the "ForgeStack service overview" dashboard is provisioned
automatically with anonymous Admin access and no login form, which is a
**local-development-only** convenience (see "Security posture" below).

### Verify each signal landed, without the UI

```bash
# Prometheus actually scraped the target:
curl -s http://localhost:9090/api/v1/targets | grep health

# Loki has log lines:
curl -s 'http://localhost:3100/loki/api/v1/query_range?query={container=~".+"}' | head -c 400

# Tempo has a trace (replace with a real trace_id from your service's logs):
curl -s http://localhost:3200/api/traces/<trace_id> | head -c 400
```

## Cleanup

```bash
docker compose -f observability/compose.yaml down      # stop, keep volumes (data survives a restart)
docker compose -f observability/compose.yaml down -v   # also delete Prometheus/Loki/Tempo/Grafana data
```

**Verify cleanup:** `docker compose -f observability/compose.yaml ps` should print no
containers; with `-v`, `docker volume ls | grep forgestack-observability` should print
nothing.

## Security posture: local development, not production monitoring

This stack's defaults are deliberately convenient for one developer on one machine,
and deliberately **not** a template for a shared or production deployment:

- **Every published port is bound to `127.0.0.1`** (`127.0.0.1:3000:3000`, etc., not
  bare `3000:3000`) in `compose.yaml`. Docker publishes a bare `host:container` port
  mapping on `0.0.0.0` by default, which would make Grafana, Prometheus, Loki, Tempo,
  and the OTel Collector's unauthenticated endpoints reachable from anyone else on your
  Wi-Fi/LAN, not just this machine. Loopback binding is the actual enforcement
  mechanism here — not a comment asking you to be careful.
- **Grafana's anonymous Admin access is survivable precisely because of that loopback
  binding**, not on its own. If you ever need this stack reachable from another
  machine (a shared team instance, a remote dev box), the loopback-only port bindings
  must be relaxed deliberately *and* Grafana's anonymous-Admin environment variables
  must be replaced with real authentication (`GF_SECURITY_ADMIN_PASSWORD` at minimum,
  SSO/OAuth for more than one operator) at the same time — never relax one without the
  other.
- **A production or shared-network monitoring stack is a different thing entirely**:
  real authentication throughout, TLS between components, retention and access-control
  policy matched to actual data sensitivity, and almost certainly a managed backend
  (Grafana Cloud, AWS CloudWatch/X-Ray, etc.) rather than five hand-configured
  containers. Nothing in this directory should be copied as-is into that context.

## Known limitations (reported honestly, not hidden)

- The Grafana dashboard's trace-correlation link (log line → Tempo trace) depends on
  the log line containing `trace_id=<hex>`, which `internal/platform/middleware`
  already writes — but only once a span exists. A request before `telemetry.Setup` ran,
  or one that hit a route with no span (there are none currently, but a future change
  could add one), would log without `trace_id` and the link simply wouldn't appear for
  that line.
- Promtail only picks up a container's logs if it has the `com.forgestack.logs=true`
  label when started; it does not retroactively attach to already-running containers it
  wasn't told to watch, and it does not see logs from a service run directly on the
  Mac (`go run`) at all — only from a container.
- This is a from-scratch config for all five pieces, not a vetted production reference
  architecture; retention, authentication, and durability are all left at
  local-development defaults.
