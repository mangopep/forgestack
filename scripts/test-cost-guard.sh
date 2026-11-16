#!/usr/bin/env bash
# End-to-end local proof for the Cost Guard foundation (internal/costguard,
# cmd/costguard): runs the package's own unit tests, then exercises the CLI
# against committed fixtures only. Never contacts Infracost, AWS, or any
# other network service -- every estimate here is a MOCK fixture, and the
# CLI's fail-closed exit code (nonzero on any non-approval) is asserted
# directly, not just eyeballed from output.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

echo "=== unit tests ==="
go test ./internal/costguard/...

test_root="$(mktemp -d)"
trap 'rm -rf "${test_root:?}"' EXIT INT TERM
budget="$test_root/budget.json"
cat >"$budget" <<'EOF'
{"max_hourly_usd": 1.0, "max_monthly_usd": 10.0, "max_lifetime": "24h", "max_estimate_age": "24h"}
EOF

echo "=== scan: NAT gateway is flagged with no pricing lookup ==="
out="$(go run ./cmd/costguard scan --plan internal/costguard/testdata/plan-nat-gateway.json)"
echo "$out"
python3 -I -c 'import json,sys; d=json.loads(sys.argv[1]); assert len(d["flagged"])==1 and d["flagged"][0]["resource_type"]=="aws_nat_gateway"' "$out"

echo "=== evaluate: a cheap MOCK fixture is still rejected (source, not price, gates approval) ==="
if go run ./cmd/costguard evaluate --estimate internal/costguard/testdata/estimate-cheap.json --budget "$budget" --ttl 1h >"$test_root/cheap.out" 2>&1; then
  echo "expected a MOCK estimate to be rejected regardless of price" >&2
  cat "$test_root/cheap.out" >&2
  exit 1
fi
grep -q "not LIVE" "$test_root/cheap.out"
echo "ok: rejected (exit nonzero), reason mentions 'not LIVE'"

echo "=== evaluate: an over-budget, unapproved-NAT-gateway MOCK fixture reports every reason at once ==="
if go run ./cmd/costguard evaluate --estimate internal/costguard/testdata/estimate-over-budget.json --budget "$budget" --plan internal/costguard/testdata/plan-nat-gateway.json --ttl 1h >"$test_root/over.out" 2>&1; then
  echo "expected rejection" >&2
  cat "$test_root/over.out" >&2
  exit 1
fi
grep -q "not LIVE" "$test_root/over.out"
grep -q "aws_nat_gateway" "$test_root/over.out"
grep -q "exceeds budget max_monthly_usd" "$test_root/over.out"
echo "ok: all three reasons present in one decision"

echo "=== evaluate: missing-pricing fixture is rejected, not treated as free ==="
if go run ./cmd/costguard evaluate --estimate internal/costguard/testdata/estimate-missing-pricing.json --budget "$budget" --ttl 1h >"$test_root/missing.out" 2>&1; then
  echo "expected rejection" >&2
  cat "$test_root/missing.out" >&2
  exit 1
fi
grep -q "no pricing data" "$test_root/missing.out"
echo "ok: rejected for missing pricing"

echo "=== evaluate: --live without INFRACOST_API_KEY reports UNAVAILABLE and still fails closed ==="
unset INFRACOST_API_KEY || true
if go run ./cmd/costguard evaluate --estimate internal/costguard/testdata/plan-cheap.json --budget "$budget" --ttl 1h --live >"$test_root/live.out" 2>&1; then
  echo "expected rejection when no API key is configured" >&2
  cat "$test_root/live.out" >&2
  exit 1
fi
grep -q "UNAVAILABLE" "$test_root/live.out"
echo "ok: --live with no API key is UNAVAILABLE, never silently approved"

echo "Cost Guard: policy engine, plan scanning, fixture-based evaluation, and the fail-closed --live-without-a-key path all passed. No pricing vendor, AWS account, or network call was used anywhere in this script."
