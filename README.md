# ForgeStack — Go + Docker + AWS + DevSecOps

**A real, progressively expanded, cost-conscious engineering learning repository for an Apple M4 Mac.**

## What runs TODAY (starter milestone)

- Go API built using the standard library (`GET /healthz`, `GET /readyz`, `GET /metrics-demo`, `GET/POST /api/v1/posts`).
- Input validation, request-body limit, bounded pagination, structured logs, safe timeouts, graceful shutdown, concurrency-safe in-memory storage.
- Unit, HTTP and concurrency tests, Docker multi-stage image, Compose API.
- Optional local PostgreSQL + Redis services (`data` profile), optional ClickHouse service (`analytics` profile); sample SQL initialization.
- CI formatting, tests, race detection, govulncheck, CodeQL, PR reviewdog, custom repository reviewer, Terraform validation/IaC checks, Dependabot.
- **Opt-in** Terraform S3 exercise that creates no AWS resources by default.

## Not yet implemented (future lessons)

**IMPORTANT:** API posts are in memory and disappear on restart. PostgreSQL, Redis, and ClickHouse are running practice services but **are not yet wired to API endpoints**. There is no authentication, user accounts, production metrics, managed deployment or AI reviewer in this starter. Do not expose this demo API publicly or use it with sensitive user data.

## Mac M4 requirements

- Apple silicon/macOS; Git; Go 1.23+ for local standard-library code (CI uses Go 1.27.4); Docker Desktop (Apple silicon) for containers; Python 3 for review script.
- AWS CLI, Terraform and kubectl are **optional for this first milestone**.
- Check what is already installed: `bash scripts/check-env.sh`.
- See `docs/COMMANDS.md` for every important command, flag, expected outcome and safe undo.

## First 5-minute run (no AWS account, no cloud bill)

```bash
# from this repository root
go test ./...
go run ./cmd/api
# in a second terminal:
curl -i http://localhost:8080/healthz
curl -i -X POST http://localhost:8080/api/v1/posts -H 'Content-Type: application/json' -d '{"author":"Nia","text":"Hello Alex"}'
curl -i http://localhost:8080/api/v1/posts
```

Stop with Ctrl+C. `make test`, `make race`, `make vet` and `make smoke` also work (smoke requires API running).

## Docker lab

```bash
make docker-up                  # API only, localhost:8080
make smoke
make docker-data                # add PostgreSQL and Redis; localhost only
make docker-analytics           # add ClickHouse; uses more RAM
make docker-down                # stop everything, retain database volumes
# ONLY if you want to permanently erase local data:
make docker-clean
```

On the M4, use the Apple-silicon Docker installer. Optional ClickHouse may require more Docker memory. The Compose ports are bound to `127.0.0.1` deliberately.

## Repository layout

- `cmd/api`, `internal/api`: Go application and tests
- `databases/postgres`: SQL lab schema
- `compose.yaml`, `Dockerfile`: local containers
- `infrastructure/terraform/aws-s3-sandbox`: safe-by-default opt-in Terraform example
- `.github/workflows`: CI, reviewer, CodeQL and Terraform checks (NO cloud auto deploy)
- `scripts/review_guard.py`: offline automated code review baseline
- `docs/ROADMAP.md`: full 25-module course, certification and failure exercises
- `docs/AUTOMATED-CODE-REVIEW.md`: security, reviewdog, branch rules and optional future AI reviewer
- `docs/COST-SAFETY.md`: cost safeguards and teardown procedure
- `docs/COMMANDS.md`: command explanations

## PR safety, code review and CI

```bash
python3 scripts/review_guard.py --ci
make test
make race
make vet
```

When pushed to GitHub, PRs run automated checks and (on same-repo PRs) reviewdog annotations. See `docs/AUTOMATED-CODE-REVIEW.md` to enable branch protection, CODEOWNERS and security features. Workflow permissions and advanced GitHub security availability depend on your repository settings. Third-party Action versions must be vetted/pinned to commit SHA for production use.

## AWS + Terraform (no automatic charges)

**AWS account not needed for initial exercises.** When ready, read `docs/COST-SAFETY.md` and `infrastructure/terraform/aws-s3-sandbox/README.md`. The Terraform sample creates zero resources unless explicitly opted in. GitHub workflows never run `terraform apply` or `destroy`.

## Learning philosophy

For each experiment: **build → test → intentionally break in your sandbox → observe → diagnose → fix → add regression test → security review → document → clean up**. Publish reproducible commits/PRs, not just screenshots. See `docs/ROADMAP.md`.

## Claude Code AI engineering assistant

This repository contains a complete `.claude/` starter with project memory (`CLAUDE.md`), coding/security/cost rules, reusable skills, read-only specialist agents and blocking hook guardrails. See [.claude/README.md](.claude/README.md).

```bash
# Run in project root, after installing/authenticating Claude Code separately:
claude
# Inside Claude Code:
/start-lab
/code-review
/security-review
```

AI code review is **available locally on demand** through `/code-review`; it is **not a live GitHub AI PR bot**. Existing GitHub PR automation uses Reviewdog, Go checks and CodeQL. A GitHub-integrated AI reviewer requires explicit installation, authorization, repository configuration and possibly usage charges.
