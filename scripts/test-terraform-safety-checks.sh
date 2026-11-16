#!/usr/bin/env bash
# Proves, locally and without touching AWS, that this module's committed
# guardrails actually reject an unsafe change before any plan/apply would
# run: a corrupted lockfile checksum, an incompatible provider version, and
# an unsupported Terraform CLI version. Never runs plan/apply/destroy and
# never contacts a cloud account -- every check below operates on a
# throwaway copy of the sandbox module with -backend=false.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src="$repo_root/infrastructure/terraform/aws-s3-sandbox"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT INT TERM

reset_copy() {
  rm -rf "${test_root:?test_root must be set}"/*
  cp -r "$src"/. "$test_root"/
  rm -rf "${test_root:?}/.terraform"
}

expect_rejected() {
  local label="$1" out="$test_root/last.out"
  if (cd "$test_root" && terraform init -backend=false -lockfile=readonly -input=false >"$out" 2>&1); then
    echo "expected $label to be rejected, but init succeeded:" >&2
    cat "$out" >&2
    exit 1
  fi
  local reason
  reason="$(grep -im1 "error" "$out" | sed 's/\x1b\[[0-9;]*m//g')"
  echo "ok: $label rejected -- ${reason:-see $out}"
}

echo "=== baseline: a clean copy still initializes and validates ==="
reset_copy
(cd "$test_root" && terraform init -backend=false -lockfile=readonly -input=false >/dev/null && terraform validate >/dev/null)
echo "ok"

echo "=== corrupted lockfile checksums are rejected ==="
reset_copy
python3 -I -c "
import re
path = '$test_root/.terraform.lock.hcl'
text = open(path).read()
# Corrupt every h1 (per-platform extracted-package hash) and zh (per-platform
# archive hash) entry, not just one -- a fresh init on this machine's
# platform only checks its own platform's entries, so leaving any other
# platform's hash correct wouldn't reliably trigger the mismatch here.
text, n1 = re.subn(r'h1:[A-Za-z0-9+/=]+', 'h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=', text)
text, n2 = re.subn(r'zh:[0-9a-f]+', 'zh:' + '0' * 64, text)
assert n1 >= 1 and n2 >= 1, f'expected h1/zh hashes to corrupt, found h1={n1} zh={n2}'
open(path, 'w').write(text)
"
expect_rejected "corrupted lockfile checksums"

echo "=== a provider version outside the committed lockfile is rejected ==="
reset_copy
python3 -I -c "
path = '$test_root/main.tf'
text = open(path).read()
text, n = text.replace('version = \"~> 6.0\"', 'version = \"~> 7.0\"', 1), 1
open(path, 'w').write(text)
"
expect_rejected "an incompatible provider version constraint"

echo "=== an unsupported Terraform CLI version constraint is rejected ==="
reset_copy
python3 -I -c "
path = '$test_root/main.tf'
text = open(path).read()
assert 'required_version = \">= 1.7.0, < 2.0.0\"' in text, 'expected required_version line not found -- main.tf changed shape'
text = text.replace('required_version = \">= 1.7.0, < 2.0.0\"', 'required_version = \">= 99.0.0\"')
open(path, 'w').write(text)
"
expect_rejected "an unsatisfiable required_version"

echo "All Terraform change-safety checks passed: a corrupted checksum, an incompatible provider version, and an unsupported CLI version are each rejected before any plan/apply would run."
