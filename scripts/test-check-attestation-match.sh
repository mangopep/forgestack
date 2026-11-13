#!/usr/bin/env bash
# Exercises scripts/check-attestation-match.py against a REAL captured
# `gh attestation verify --format json` result (scripts/testdata/
# real-attestation-fixture.json -- the actual attestation for the SBOM
# files signed on main commit 90da9d887cafb8324c3b2d9826dba52ed57c1ec3,
# fetched live from supply-chain-attest.yml's successful run (databaseId
# 43454395425) plus deliberately malformed/mismatched variants. Runs
# entirely offline: it never calls `gh attestation verify` itself, so it
# publishes nothing and needs no credentials.
#
# The real fixture has THREE subjects in one statement -- this is
# exactly the shape that would have silently passed the wrong check if
# the matching logic ever went back to trusting `subject[0]`: the
# "second real subject" case below matches on
# sbom-go.cdx.json's digest, which is NOT first in the list.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
checker="$script_dir/check-attestation-match.py"
fixture="$script_dir/testdata/real-attestation-fixture.json"
fixture_json="$(cat "$fixture")"

# Real values from the fixture.
real_commit="90da9d887cafb8324c3b2d9826dba52ed57c1ec3"
real_ref="refs/heads/main"
real_digest_first="sha256:85853c18129299c9fa046f3899b0a9b8cef202cc8b5902d2bbc89daf27d2a1b5"
real_digest_second="sha256:cbfebf35588e75a291083f19fb0910fb6d9500d19ef3b54224eca9d8de9f3b3a"

pass_count=0
fail_count=0

expect_ok() {
  local description="$1"
  shift
  if python3 -I "$checker" "$@" >/tmp/check-attestation-match-test-output.txt 2>&1; then
    echo "PASS: $description"
    pass_count=$((pass_count + 1))
  else
    echo "FAIL: $description (expected success, got failure)"
    cat /tmp/check-attestation-match-test-output.txt
    fail_count=$((fail_count + 1))
  fi
}

expect_fail() {
  local description="$1"
  shift
  if python3 -I "$checker" "$@" >/tmp/check-attestation-match-test-output.txt 2>&1; then
    echo "FAIL: $description (expected failure, got success)"
    cat /tmp/check-attestation-match-test-output.txt
    fail_count=$((fail_count + 1))
  else
    echo "PASS: $description"
    pass_count=$((pass_count + 1))
  fi
}

echo "--- Positive cases (real attestation fixture) ---"
expect_ok "the first real subject's digest, with the correct commit and ref, matches" \
  "$fixture_json" "$real_digest_first" "$real_commit" "$real_ref" "test"
expect_ok "a REAL subject that is NOT index 0 still matches (the bug this logic fixes)" \
  "$fixture_json" "$real_digest_second" "$real_commit" "$real_ref" "test"

echo "--- Negative cases ---"
expect_fail "a digest that matches no subject in the attestation fails closed" \
  "$fixture_json" "sha256:0000000000000000000000000000000000000000000000000000000000000000" "$real_commit" "$real_ref" "test"
expect_fail "the right digest but the wrong source commit fails closed" \
  "$fixture_json" "$real_digest_first" "0000000000000000000000000000000000000000" "$real_ref" "test"
expect_fail "the right digest but the wrong ref fails closed" \
  "$fixture_json" "$real_digest_first" "$real_commit" "refs/heads/some-other-branch" "test"
expect_fail "an empty attestation result (missing attestation) fails closed" \
  "[]" "$real_digest_first" "$real_commit" "$real_ref" "test"
expect_fail "malformed JSON fails closed" \
  "not json" "$real_digest_first" "$real_commit" "$real_ref" "test"
expect_fail "a JSON object instead of a list fails closed" \
  '{"not":"a list"}' "$real_digest_first" "$real_commit" "$real_ref" "test"
expect_fail "an expected digest without the sha256: prefix is rejected outright" \
  "$fixture_json" "85853c18129299c9fa046f3899b0a9b8cef202cc8b5902d2bbc89daf27d2a1b5" "$real_commit" "$real_ref" "test"
expect_fail "an attestation entry missing sourceRepositoryDigest fails closed" \
  '[{"verificationResult":{"statement":{"subject":[{"digest":{"sha256":"85853c18129299c9fa046f3899b0a9b8cef202cc8b5902d2bbc89daf27d2a1b5"}}]},"signature":{"certificate":{"sourceRepositoryRef":"refs/heads/main"}}}}]' \
  "$real_digest_first" "$real_commit" "$real_ref" "test"
expect_fail "an attestation entry missing sourceRepositoryRef fails closed" \
  '[{"verificationResult":{"statement":{"subject":[{"digest":{"sha256":"85853c18129299c9fa046f3899b0a9b8cef202cc8b5902d2bbc89daf27d2a1b5"}}]},"signature":{"certificate":{"sourceRepositoryDigest":"90da9d887cafb8324c3b2d9826dba52ed57c1ec3"}}}}]' \
  "$real_digest_first" "$real_commit" "$real_ref" "test"

echo
echo "$pass_count passed, $fail_count failed"
[ "$fail_count" -eq 0 ]
