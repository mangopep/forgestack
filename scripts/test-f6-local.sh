#!/usr/bin/env bash
# End-to-end local proof: resolve modules, generate/build/run a service, then
# clean its owned resources and verify an unrelated sibling survives.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
server_pid=""
cleanup() {
  if [ -n "$server_pid" ]; then kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
  rm -rf "$test_root"
}
trap cleanup EXIT INT TERM

export GOCACHE="${GOCACHE:-$test_root/go-cache}"
env_root="$test_root/environments"
mkdir -p "$env_root/unrelated"
printf 'preserve\n' > "$env_root/unrelated/sentinel.txt"

(cd "$repo_root" && go run ./cmd/modulecheck resolve modules/go-api/module.json modules/docker-compose/module.json > "$test_root/composition.json")
python3 -I -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["connections"]["go-api"] == ["docker-compose:deployment.docker-compose"]' "$test_root/composition.json"

(cd "$repo_root" && go run ./cmd/playground create --root "$env_root" --owner f6-test --modules go-api,docker-compose --ttl 1h > "$test_root/environment.json")
env_id="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$test_root/environment.json")"
service_dir="$env_root/$env_id/resources/app"
(cd "$repo_root" && scripts/new-service.sh go "$service_dir" --name f6-sample-api --port 18081 --module example.com/f6-sample-api --features base,docker,ci --validate)
(cd "$service_dir" && go build -o "$test_root/api" ./cmd/api)
PORT=18081 "$test_root/api" > "$test_root/api.log" 2>&1 &
server_pid=$!
ready=0
for _ in $(seq 1 40); do
  if curl --silent --fail http://127.0.0.1:18081/healthz > "$test_root/health.json"; then ready=1; break; fi
  sleep 0.25
done
if [ "$ready" -ne 1 ]; then cat "$test_root/api.log" >&2; echo "generated service failed to become healthy" >&2; exit 1; fi
python3 -I -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["status"] == "ok" and d["service"] == "f6-sample-api"' "$test_root/health.json"
kill "$server_pid"
wait "$server_pid" || true
server_pid=""

(cd "$repo_root" && go run ./cmd/playground cleanup --root "$env_root" --owner f6-test --id "$env_id" > "$test_root/cleanup.json")
python3 -I -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["cleanup"] == "VERIFIED CLEAN" and d["environment"]["status"] == "verified_clean"' "$test_root/cleanup.json"
test "$(cat "$env_root/unrelated/sentinel.txt")" = preserve
test ! -e "$service_dir"
test -f "$env_root/$env_id/environment.json"
echo "F6 local module composition, standalone generation, health check, ownership, and cleanup passed"
