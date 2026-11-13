#!/usr/bin/env bash
# Re-reads a published build-provenance attestation independently (the
# same way a consumer verifying the release later would, via `gh
# attestation verify`) and asserts, explicitly, that its subject digest,
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

result="$(gh attestation verify "oci://${SUBJECT_NAME}@${BARE_DIGEST}" --repo "$REPO" --format json)"

python3 -I -c '
import json, sys

result = json.loads(sys.argv[1])
expected_digest = sys.argv[2].removeprefix("sha256:")
expected_commit = sys.argv[3]
expected_ref = sys.argv[4]
label = sys.argv[5]

if not result:
    print(f"FAIL: {label}: gh attestation verify returned no results", file=sys.stderr)
    sys.exit(1)

entry = result[0]
statement = entry.get("verificationResult", {}).get("statement", {})
subjects = statement.get("subject", [])
actual_digest = subjects[0].get("digest", {}).get("sha256") if subjects else None
if actual_digest != expected_digest:
    print(f"FAIL: {label}: attested subject digest ({actual_digest}) does not match the published image digest ({expected_digest})", file=sys.stderr)
    sys.exit(1)

cert = entry.get("verificationResult", {}).get("signature", {}).get("certificate", {})
actual_commit = cert.get("sourceRepositoryDigest")
if actual_commit != expected_commit:
    print(f"FAIL: {label}: attested source commit ({actual_commit}) does not match the expected commit ({expected_commit})", file=sys.stderr)
    sys.exit(1)

actual_ref = cert.get("sourceRepositoryRef")
if actual_ref != expected_ref:
    print(f"FAIL: {label}: attested ref ({actual_ref}) does not match the expected release ref ({expected_ref})", file=sys.stderr)
    sys.exit(1)

print(f"OK: {label}: attestation subject digest, source commit, and ref all match.")
' "$result" "$BARE_DIGEST" "$EXPECTED_COMMIT" "$EXPECTED_REF" "$LABEL"
