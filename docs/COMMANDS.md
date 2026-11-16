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
| `go run ./cmd/modulecheck resolve modules/go-api/module.json modules/docker-compose/module.json` | Resolve declared module capabilities and provider versions | stable JSON listing selected modules and the optional Compose connection | Read-only; incompatible combinations exit nonzero |
| `go run ./cmd/playground create --root /tmp/forgestack-envs --owner "$USER" --modules go-api,docker-compose --ttl 4h` | Create a local playground ownership record and private `resources/` directory; `--ttl` is limited to 24h | JSON with `env-<id>`, owner, selected modules, creation/expiration times and `active` status | Local only; use returned ID with `playground cleanup`; no resources beyond that directory are created |
| `scripts/new-service.sh go /tmp/forgestack-envs/<ID>/resources/app --name sample-api --port 18081 --module example.com/sample-api --features base,docker,ci --validate` | Generate and independently build/test a standalone Go service; `--features` selects optional files | `Validation passed.` and a standalone project | Destination must not exist; remove it only through its owning playground cleanup |
| `go run ./cmd/playground cleanup --root /tmp/forgestack-envs --owner "$USER" --id env-<16-hex>` | Remove only the local resources folder for the matching playground owner and record verification | `VERIFIED CLEAN`, `CLEANUP INCOMPLETE`, or `CLEANUP UNVERIFIED` | Persistent `product` mode is rejected; inspect retained `environment.json` after cleanup |

Every new lab expands this glossary.
