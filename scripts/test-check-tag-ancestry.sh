#!/usr/bin/env bash
# Exercises scripts/check-tag-ancestry.sh against real git fixtures:
# a valid main commit, a feature-only commit never merged, a commit that
# doesn't exist, a ref that doesn't exist, and a shallow clone. Run this
# whenever check-tag-ancestry.sh changes -- it is not wired into CI
# automatically (it creates and clones temporary repositories, which is
# unnecessary overhead for every ordinary PR), but should be run by hand
# before any change to the ancestry guard itself.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
guard="$script_dir/check-tag-ancestry.sh"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

pass_count=0
fail_count=0

expect() {
  local description="$1" expected_exit="$2"
  shift 2
  set +e
  "$@" >/tmp/tag-ancestry-test-output.txt 2>&1
  local actual_exit=$?
  set -e
  if [ "$actual_exit" -eq "$expected_exit" ]; then
    echo "PASS: $description"
    pass_count=$((pass_count + 1))
  else
    echo "FAIL: $description (expected exit $expected_exit, got $actual_exit)"
    cat /tmp/tag-ancestry-test-output.txt
    fail_count=$((fail_count + 1))
  fi
}

cd "$work_dir"
git init -q -b main repo-origin
cd repo-origin
git config user.email "test@example.com"
git config user.name "Test"
echo c1 > f.txt && git add f.txt && git commit -q -m c1
main_commit="$(git rev-parse HEAD)"
git checkout -q -b feature
echo f1 > g.txt && git add g.txt && git commit -q -m feature-only
feature_commit="$(git rev-parse HEAD)"
git checkout -q main

expect "a real main commit is reachable from main" 0 \
  "$guard" "$main_commit" main

expect "a feature-only commit never merged to main is rejected" 1 \
  "$guard" "$feature_commit" main

expect "a nonexistent commit SHA fails closed" 1 \
  "$guard" "0000000000000000000000000000000000000000" main

expect "a nonexistent main-ref fails closed" 1 \
  "$guard" "$main_commit" does-not-exist

cd "$work_dir"
git clone -q --depth 1 --branch main "file://$work_dir/repo-origin" repo-shallow
cd repo-shallow
expect "a shallow clone fails closed, even for an otherwise-real commit" 1 \
  "$guard" "$(git rev-parse HEAD)" main

echo
echo "$pass_count passed, $fail_count failed"
[ "$fail_count" -eq 0 ]
