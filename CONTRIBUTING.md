# Contributing to ForgeStack

Thanks for considering a contribution. This project is a teaching-oriented, cost-conscious
Go/cloud engineering lab, and contributions are expected to keep that spirit: small, tested,
explained, and safe to run on a laptop with no cloud bill.

## Before you start

- Read `README.md` for what currently works, and `docs/ROADMAP.md` for planned direction.
- Read `CLAUDE.md` and `.claude/rules/` if you use Claude Code — they describe the working
  agreements this repo follows (small steps, explicit tradeoffs, no silent destructive actions).
- Open an issue first for anything beyond a small fix, so the approach can be agreed before you
  invest time.

## Development setup

```bash
go test ./...
go run ./cmd/api
```

See `docs/COMMANDS.md` for the full command glossary and `README.md` for the Docker lab.

## Pull requests

- Keep PRs small and focused; one logical change per PR.
- Include tests for new behavior (unit, HTTP, and concurrency tests where relevant).
- Run before opening a PR:
  ```bash
  gofmt -l .
  go vet ./...
  go test -race ./...
  python3 scripts/review_guard.py --ci
  ```
- Fill out the PR template completely, including the security and cost review checklists.
- Do not include infrastructure changes that create billable AWS resources without discussion;
  the Terraform sandbox under `infrastructure/terraform/aws-s3-sandbox` must stay opt-in
  (`enable_resources = false` by default).

## Code style

- Go: standard library first, justify any new dependency in the PR description.
- Keep business logic independent of HTTP/database glue; return structured errors without
  leaking internals.
- No secrets, tokens, personal data, or real credentials in code, tests, fixtures, or commit
  messages — use `.env.example` patterns and fake data.

## Security

Do not open a public issue for a suspected vulnerability. See `SECURITY.md` for how to report it.

## Code of Conduct

This project follows the `CODE_OF_CONDUCT.md`. Participation implies agreement to it.
