#!/usr/bin/env bash
# Picks the one and only entry out of a `docker inspect --format
# '{{json .RepoDigests}}'` JSON array, failing closed if there isn't
# exactly one. An image normally has exactly one RepoDigests entry per
# registry it was pushed to and then inspected against; zero means the
# push didn't actually register locally (missing), and more than one
# means this script can't tell which digest the caller actually wants
# (ambiguous) -- neither case should be resolved by guessing index 0 and
# hoping, which is what release.yml did before this fix.
#
# Usage: select-single-repo-digest.sh <repo-digests-json-array> <label>
#   e.g. select-single-repo-digest.sh '["ghcr.io/x/y@sha256:abc..."]' "Go image"
set -euo pipefail

usage() {
  echo "usage: $0 <repo-digests-json-array> <label>" >&2
  exit 2
}

[ $# -eq 2 ] || usage
JSON="$1"
LABEL="$2"

count="$(python3 -I -c '
import json, sys
try:
    data = json.loads(sys.argv[1])
except Exception as e:
    print(f"PARSE_ERROR: {e}")
    sys.exit(0)
if not isinstance(data, list):
    print("NOT_A_LIST")
    sys.exit(0)
print(len(data))
' "$JSON")"

case "$count" in
PARSE_ERROR:*)
  echo "FAIL: $LABEL: RepoDigests is not valid JSON: $count (raw: '$JSON')" >&2
  exit 1
  ;;
NOT_A_LIST)
  echo "FAIL: $LABEL: RepoDigests is not a JSON array (raw: '$JSON')" >&2
  exit 1
  ;;
0)
  echo "FAIL: $LABEL: RepoDigests is empty -- the push did not register a digest locally, refusing to proceed (missing)." >&2
  exit 1
  ;;
1)
  : # exactly one entry, the expected case
  ;;
*)
  echo "FAIL: $LABEL: RepoDigests has $count entries, expected exactly 1 -- refusing to guess which one was actually pushed (ambiguous): $JSON" >&2
  exit 1
  ;;
esac

python3 -I -c 'import json, sys; print(json.loads(sys.argv[1])[0])' "$JSON"
