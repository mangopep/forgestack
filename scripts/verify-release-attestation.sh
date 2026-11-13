#!/usr/bin/env bash
# Re-reads a published build-provenance attestation independently (the
# same way a consumer verifying the release later would, via `gh
# attestation verify`) and hands the result to
# scripts/check-attestation-match.py, which asserts its subject digest,
# source commit, and workflow ref all match what this release run
# actually published -- rather than trusting that
# actions/attest-build-provenance did the right thing without checking.
#
# Usage: verify-release-attestation.sh <subject-name> <bare-digest> \
#          <expected-commit> <expected-ref> <label> <repo>
#   e.g. verify-release-attestation.sh ghcr.io/mangopep/forgestack \
#          sha256:<64 hex> "$GITHUB_SHA" refs/tags/v0.1.1 "Go image" mangopep/forgestack
set -euo pipefail

usage() {
  echo "usage: $0 <subject-name> <bare-digest> <expected-commit> <expected-ref> <label> <repo>" >&2
  exit 2
}

[ $# -eq 6 ] || usage
SUBJECT_NAME="$1"
BARE_DIGEST="$2"
EXPECTED_COMMIT="$3"
EXPECTED_REF="$4"
LABEL="$5"
REPO="$6"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

result="$(gh attestation verify "oci://${SUBJECT_NAME}@${BARE_DIGEST}" --repo "$REPO" --format json)"

python3 -I "$script_dir/check-attestation-match.py" \
  "$result" "$BARE_DIGEST" "$EXPECTED_COMMIT" "$EXPECTED_REF" "$LABEL"
