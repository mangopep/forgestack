#!/usr/bin/env bash
set -euo pipefail
base="${BASE_URL:-http://localhost:8080}"
curl --fail --silent --show-error "${base}/healthz"
echo
curl --fail --silent --show-error -X POST "${base}/api/v1/posts" -H 'Content-Type: application/json' -d '{"author":"Nia","text":"Hello from the smoke test"}'
echo
curl --fail --silent --show-error "${base}/api/v1/posts?limit=5"
echo
