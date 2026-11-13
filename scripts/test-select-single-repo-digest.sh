#!/usr/bin/env bash
# Exercises scripts/select-single-repo-digest.sh against the shapes
# `docker inspect --format '{{json .RepoDigests}}'` can actually return:
# the normal single-entry case (using the real v0.1.1 Go image digest),
# an empty array (missing), multiple entries (ambiguous), and malformed
# JSON.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
selector="$script_dir/select-single-repo-digest.sh"

pass_count=0
fail_count=0

expect_ok() {
  local description="$1" json="$2" label="$3" expected_output="$4"
  local actual_output
  if actual_output="$("$selector" "$json" "$label" 2>/tmp/select-single-repo-digest-test-output.txt)"; then
    if [ "$actual_output" = "$expected_output" ]; then
      echo "PASS: $description"
      pass_count=$((pass_count + 1))
    else
      echo "FAIL: $description (expected output '$expected_output', got '$actual_output')"
      fail_count=$((fail_count + 1))
    fi
  else
    echo "FAIL: $description (expected success, got failure)"
    cat /tmp/select-single-repo-digest-test-output.txt
    fail_count=$((fail_count + 1))
  fi
}

expect_fail() {
  local description="$1" json="$2" label="$3"
  if "$selector" "$json" "$label" >/tmp/select-single-repo-digest-test-output.txt 2>&1; then
    echo "FAIL: $description (expected failure, got success)"
    cat /tmp/select-single-repo-digest-test-output.txt
    fail_count=$((fail_count + 1))
  else
    echo "PASS: $description"
    pass_count=$((pass_count + 1))
  fi
}

echo "--- Normal case (real digest from the live v0.1.1 attempt) ---"
expect_ok "exactly one RepoDigests entry is selected" \
  '["ghcr.io/mangopep/forgestack@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27"]' \
  "Go image" \
  "ghcr.io/mangopep/forgestack@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27"

echo "--- Missing (empty array) ---"
expect_fail "an empty RepoDigests array fails closed instead of guessing" \
  '[]' "Go image"

echo "--- Ambiguous (more than one entry) ---"
expect_fail "two RepoDigests entries fails closed instead of picking index 0" \
  '["ghcr.io/mangopep/forgestack@sha256:b27b2bc3335740bfd1329c6e227015d41b2d4006959966d0f9758fe40e7f5b27","ghcr.io/mangopep/forgestack@sha256:421e1663089d124185fbfcd2e3bbd34886dbb86cc3b896d185b155bcf7a3f7f2"]' \
  "Go image"

echo "--- Malformed input ---"
expect_fail "invalid JSON fails closed" \
  'not json at all' "Go image"
expect_fail "a JSON object instead of an array fails closed" \
  '{"not":"a list"}' "Go image"

echo
echo "$pass_count passed, $fail_count failed"
[ "$fail_count" -eq 0 ]
