# __SERVICE_NAME__

Generated from a ForgeStack Go service template
(https://github.com/mangopep/forgestack/tree/main/templates/go-service). This project does
not depend on ForgeStack's repository, CI, or configuration in any way — it is a complete,
standalone starting point.

## What's included

- `GET /healthz`, `GET /readyz` — service metadata + readiness, see
  `internal/platform/router`.
- Request-correlation IDs (`X-Request-Id`) and structured JSON logging —
  `internal/platform/middleware`.
- Safe error wrapping — `internal/platform/apierror`.
- Graceful shutdown on SIGINT/SIGTERM — `cmd/api/main.go`.
- A hardened Dockerfile (non-root, digest-pinned base image) and `compose.yaml`
  (`cap_drop: [ALL]`, read-only filesystem).
- CI (`.github/workflows/ci.yml`): formatting, vet, race-detected tests, `govulncheck`,
  `gitleaks`, and a Trivy container scan — self-contained, no reference to any other
  repository.

## Run it

```bash
go test ./...
go run ./cmd/api
curl -i http://localhost:__PORT__/healthz
```

Or containerized:

```bash
docker compose up --build -d
curl -i http://localhost:__PORT__/healthz
docker compose down
```

## Add your own routes

Register them in `internal/platform/router.New()` alongside `/healthz`/`/readyz` — see the
comment there.

## If a security check fails

`ci.yml`'s `security` job (`govulncheck`) or `container` job (Trivy) failing means a real,
currently-known vulnerability was found — not a flake to retry past:

- **`govulncheck` fails**: a dependency in `go.mod` has a known, *reachable* vulnerability
  (govulncheck only flags ones your code actually calls into, not every transitive CVE).
  Run `go list -m -u all` to see what's outdated, bump the flagged module
  (`go get <module>@latest`, or a specific patched version), then `go mod tidy` and
  `go test ./...` before trusting the bump.
- **Trivy (container scan) fails**: the base image (`golang:...` for the build stage,
  `gcr.io/distroless/static-debian12:nonroot` for the runtime stage) has a known CVE.
  Re-run locally with no severity filter to see everything, not just what's gating the
  build: `trivy image --no-progress <image>:latest`. Re-resolve the digest for whichever
  stage is affected (see the `Dockerfile`'s own comment) and rebuild.
- **Never** raise the severity threshold or add a blanket suppression just to make a red
  check green. If a finding genuinely has no fix yet, that's a real, documented exception
  with an owner and a review date — not something to silently suppress.

## Upgrading from a newer version of this template

This project was generated once; it does not automatically track template changes. See
ForgeStack's `docs/VERSIONING.md` for how template versions are tagged and what a
compatible vs. breaking template change means, if you want to selectively pull in a later
template's improvements by hand.
