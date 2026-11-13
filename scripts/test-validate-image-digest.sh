#!/usr/bin/env bash
# Exercises scripts/validate-image-digest.sh against real and malformed
# image references. The two positive cases are the ACTUAL digests from
# the real v0.1.1 attempt (ghcr.io/mangopep/forgestack and
# ghcr.io/mangopep/forgestack-python-api, both independently re-pulled
# and verified after that run) -- not synthetic hex, so a regression here
# would be caught against the exact shape of reference this script must
# handle in production.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
validator="$script_dir/validate-image-digest.sh"

pass_count=0
fail_count=0

expect_ok() {
  local description="$1" input="$2" expected_output="$3"
  local actual_output
  if actual_output="$("$validator" "$input" 2>/tmp/validate-image-digest-test-output.txt)"; then
    if [ "$actual_output" = "$expected_output" ]; then
      echo "PASS: $description"
      pass_count=$((pass_count + 1))
    else
      echo "FAIL: $description (expected output '$expected_output', got '$actual_output')"
      fail_count=$((fail_count + 1))
    fi
  else
    echo "FAIL: $description (expected success, got failure)"
    cat /tmp/validate-image-digest-test-output.txt
    fail_count=$((fail_count + 1))
  fi
}

expect_fail() {
  local description="$1" input="$2"
  if "$validator" "$input" >/tmp/validate-image-digest-test-output.txt 2>&1; then
    echo "FAIL: $description (expected failure, got success)"
    cat /tmp/validate-image-digest-test-output.txt
    fail_count=$((fail_count + 1))
  else
    echo "PASS: $description"
    pass_count=$((pass_count + 1))
  fi
}

echo "--- Positive cases (real digests from the live v0.1.1 attempt) ---"
expect_ok "the real Go image's full reference extracts its bare digest" \
  "ghcr.io/mangopep/forgestack@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27" \
  "sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27"
expect_ok "the real Python image's full reference extracts its bare digest" \
  "ghcr.io/mangopep/forgestack-python-api@sha256:421e1663089d124185fbfcd2e3bbd34886dbb86cc3b896d185b155bcf7a3f7f2" \
  "sha256:421e1663089d124185fbfcd2e3bbd34886dbb86cc3b896d185b155bcf7a3f7f2"

echo "--- Negative cases ---"
expect_fail "an empty reference is rejected" ""
expect_fail "a name with no '@' at all is rejected (this is v0.1.1's real bug shape with the digest dropped)" \
  "ghcr.io/mangopep/forgestack"
expect_fail "a bare digest with no name before '@' is rejected" \
  "@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27"
expect_fail "two '@' characters is rejected as ambiguous, not guessed at" \
  "ghcr.io/x@y@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27"
expect_fail "a digest one hex character short is rejected" \
  "ghcr.io/mangopep/forgestack@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b2"
expect_fail "a digest one hex character too long is rejected" \
  "ghcr.io/mangopep/forgestack@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b277"
expect_fail "uppercase hex is rejected" \
  "ghcr.io/mangopep/forgestack@sha256:B27B2BC3335740BFD1329C6E227015D41B2D4006959966D0F9758FE40E7F5B27"
expect_fail "a non-sha256 algorithm prefix is rejected" \
  "ghcr.io/mangopep/forgestack@sha512:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27"
expect_fail "trailing garbage after a valid hex digest is rejected" \
  "ghcr.io/mangopep/forgestack@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27extra"
expect_fail "embedded whitespace is rejected" \
  "ghcr.io/mangopep/forgestack @sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27"
expect_fail "trailing whitespace is rejected" \
  "ghcr.io/mangopep/forgestack@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27 "

echo
echo "$pass_count passed, $fail_count failed"
[ "$fail_count" -eq 0 ]
