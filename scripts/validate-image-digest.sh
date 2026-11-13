#!/usr/bin/env bash
# Validates a full `name@sha256:<hex>` image reference and prints just the
# bare `sha256:<64-hex>` portion that GitHub's attest-build-provenance
# action requires for its `subject-digest` input.
#
# v0.1.1's real release failure was passing the FULL reference (registry
# and repository prefix included -- exactly what
# `docker inspect --format='{{index .RepoDigests 0}}'` returns) into that
# input, which only accepts the bare digest:
#   Error: subject-digest must be in the format "sha256:<hex-digest>"
# This is the one place that extraction happens, so it is validated once,
# strictly, and fails closed on anything malformed rather than guessing or
# silently truncating.
#
# Usage: validate-image-digest.sh <full-image-reference>
#   e.g. validate-image-digest.sh "ghcr.io/mangopep/forgestack@sha256:<64 hex>"
set -euo pipefail

usage() {
  echo "usage: $0 <full-image-reference>" >&2
  exit 2
}

[ $# -eq 1 ] || usage
REF="$1"

if [ -z "$REF" ]; then
  echo "FAIL: empty image reference -- nothing to validate (missing digest)." >&2
  exit 1
fi

# A well-formed reference is a single token -- reject embedded whitespace
# or newlines outright rather than letting them ride into a later command.
case "$REF" in
*[[:space:]]*)
  echo "FAIL: image reference contains whitespace -- not a valid single reference: '$REF'" >&2
  exit 1
  ;;
esac

# Must contain exactly one '@', separating the name from the digest. Zero
# means no digest was present at all; more than one is ambiguous -- both
# fail closed instead of picking a side.
at_count="$(grep -o '@' <<<"$REF" | wc -l | tr -d ' ')"
if [ "$at_count" -ne 1 ]; then
  echo "FAIL: image reference must contain exactly one '@' separating name from digest, found $at_count: '$REF'" >&2
  exit 1
fi

name_part="${REF%@*}"
digest_part="${REF#*@}"

if [ -z "$name_part" ]; then
  echo "FAIL: image reference has no name before '@': '$REF'" >&2
  exit 1
fi

# Strict: exactly "sha256:" followed by exactly 64 lowercase hex
# characters, nothing more and nothing less. Wrong length, uppercase,
# another algorithm, or trailing garbage all fail closed.
if ! [[ "$digest_part" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "FAIL: digest portion is not a valid sha256:<64-hex> value: '$digest_part'" >&2
  exit 1
fi

echo "$digest_part"
