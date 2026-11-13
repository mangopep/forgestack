#!/usr/bin/env bash
# Exercises the release pipeline's locally-testable pieces end to end:
# version embedding (both languages), image naming validity, and the
# ancestry guard. Does NOT touch GHCR, GitHub Releases, or any tag --
# everything here builds local-only images and runs the same scripts
# release.yml itself calls. Run this whenever release.yml, either
# Dockerfile, or scripts/verify-release-image-version.sh changes.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

pass_count=0
fail_count=0

expect() {
  local description="$1" expected_exit="$2"
  shift 2
  set +e
  "$@" >/tmp/release-pipeline-test-output.txt 2>&1
  local actual_exit=$?
  set -e
  if [ "$actual_exit" -eq "$expected_exit" ]; then
    echo "PASS: $description"
    pass_count=$((pass_count + 1))
  else
    echo "FAIL: $description (expected exit $expected_exit, got $actual_exit)"
    cat /tmp/release-pipeline-test-output.txt
    fail_count=$((fail_count + 1))
  fi
}

test_version="v0.0.0-pipeline-test"

echo "--- Building test images with the version build-arg ---"
docker build --build-arg "VERSION=${test_version}" -t forgestack:pipeline-test . >/dev/null
docker build --build-arg "VERSION=${test_version}" -t forgestack-python-api:pipeline-test apps/python-api >/dev/null

echo "--- Version embedding ---"
expect "Go image reports the real build-arg version, not 'dev'" 0 \
  "$script_dir/verify-release-image-version.sh" forgestack:pipeline-test 8070 "$test_version"
expect "Python image reports the real build-arg version, not 'docker-build'" 0 \
  "$script_dir/verify-release-image-version.sh" forgestack-python-api:pipeline-test 8071 "$test_version"

echo "--- Version embedding failure detection (negative case) ---"
docker build -t forgestack:pipeline-test-no-arg . >/dev/null
expect "the version-check script itself fails when the image was NOT built with the version arg" 1 \
  "$script_dir/verify-release-image-version.sh" forgestack:pipeline-test-no-arg 8072 "$test_version"

echo "--- Image naming (GHCR requires all-lowercase paths) ---"
expect "a lowercase repository path is accepted by Docker's own tag validation" 0 \
  docker inspect forgestack:pipeline-test
if docker tag forgestack:pipeline-test "GHCR.io/Mangopep/Forgestack:${test_version}" 2>/tmp/release-pipeline-test-output.txt; then
  echo "FAIL: Docker should reject an uppercase image reference, but it did not"
  fail_count=$((fail_count + 1))
else
  echo "PASS: Docker itself rejects an uppercase image reference (confirms why release.yml lowercases the path before using it)"
  pass_count=$((pass_count + 1))
fi

echo "--- Ancestry guard (full suite, already covers partial-failure-adjacent cases) ---"
if "$script_dir/test-check-tag-ancestry.sh"; then
  echo "PASS: ancestry guard test suite"
  pass_count=$((pass_count + 1))
else
  echo "FAIL: ancestry guard test suite"
  fail_count=$((fail_count + 1))
fi

echo "--- Attestation digest handling (the v0.1.1 bug: full reference vs. bare digest) ---"
if "$script_dir/test-validate-image-digest.sh"; then
  echo "PASS: validate-image-digest test suite"
  pass_count=$((pass_count + 1))
else
  echo "FAIL: validate-image-digest test suite"
  fail_count=$((fail_count + 1))
fi
if "$script_dir/test-select-single-repo-digest.sh"; then
  echo "PASS: select-single-repo-digest test suite"
  pass_count=$((pass_count + 1))
else
  echo "FAIL: select-single-repo-digest test suite"
  fail_count=$((fail_count + 1))
fi

echo "--- Cleanup ---"
docker rm -f \
  "$(docker ps -aq --filter ancestor=forgestack:pipeline-test)" \
  "$(docker ps -aq --filter ancestor=forgestack-python-api:pipeline-test)" \
  "$(docker ps -aq --filter ancestor=forgestack:pipeline-test-no-arg)" \
  2>/dev/null || true
docker image rm forgestack:pipeline-test forgestack-python-api:pipeline-test forgestack:pipeline-test-no-arg >/dev/null 2>&1 || true

echo
echo "$pass_count passed, $fail_count failed"
[ "$fail_count" -eq 0 ]
