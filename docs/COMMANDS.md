# Command glossary: meaning, why, output, undo

| Command | Meaning | What to expect | Undo / caution |
|---|---|---|---|
| `uname -m` | `uname` prints OS info; `-m` asks CPU architecture | `arm64` on Apple M4 | Read-only |
| `command -v go` | Find executable in current PATH | tool path or no result | Read-only |
| `go run ./cmd/api` | Compile and run local Go API | process listening at :8080 | Control-C |
| `go test -race ./...` | Run tests and Go race detector across packages | PASS or a diagnostic | Read-only, may use CPU |
| `go vet ./...` | Find suspicious Go coding patterns | no output on success | Read-only |
| `docker compose up --build -d` | Build images, start containers in background (`-d`) | container IDs | `docker compose down` |
| `docker compose --profile data up -d` | Also enable PostgreSQL and Redis | running services | `docker compose --profile data down` |
| `docker compose logs -f api` | Follow API logs (`-f`) | request lines | Control-C |
| `docker compose down` | Stop and remove Compose containers/network | stopped stack | local volumes retained |
| `docker compose down -v` | Also delete named volumes | erased databases | irreversible for unbacked data |
| `curl -i http://localhost:8080/healthz` | Fetch endpoint; `-i` includes response headers | HTTP 200 JSON | Read-only |
| `aws sts get-caller-identity` | Show AWS identity for current CLI credentials | account and ARN | Read-only; don't share identifiers publicly |
| `terraform plan` | Preview proposed infrastructure changes | change plan | Read-only to infrastructure (may refresh state) |
| `terraform apply` | Make proposed changes real | created/updated resources | MAY COST MONEY, review first |
| `terraform destroy` | Delete managed resources in active Terraform state | deletion plan | verify other resources remain |

Every new lab expands this glossary.
