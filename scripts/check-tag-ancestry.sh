#!/usr/bin/env bash
# Fails closed unless <commit> is a real ancestor of <main-ref> (default
# origin/main) -- i.e. the tag being released was actually reached by a
# real promotion to main, not an arbitrary commit someone tagged on a
# side branch. "Fails closed" here means: any condition this script
# cannot confidently verify (a missing ref, a shallow clone that could
# give a false negative) is treated as FAILURE, not skipped or assumed
# passing.
#
# Usage: scripts/check-tag-ancestry.sh <commit-sha> [main-ref]
set -euo pipefail

usage() {
  echo "usage: $0 <commit-sha> [main-ref (default: origin/main)]" >&2
  exit 2
}

[ $# -ge 1 ] || usage
COMMIT="$1"
MAIN_REF="${2:-origin/main}"

# Fail closed on a shallow clone: git merge-base --is-ancestor can give a
# false negative (reporting "not an ancestor" for a commit that actually
# IS one) if history was truncated before the real common ancestor. A
# shallow repository can never be trusted to answer this question
# correctly, so refuse outright rather than risk a false "pass".
if git rev-parse --is-shallow-repository 2>/dev/null | grep -q true; then
  echo "FAIL (closed): this is a shallow git clone -- ancestry cannot be verified reliably. Fetch full history (fetch-depth: 0) before running this check." >&2
  exit 1
fi

if ! git rev-parse --verify "$MAIN_REF" >/dev/null 2>&1; then
  echo "FAIL (closed): '$MAIN_REF' does not exist locally -- cannot verify ancestry against a ref that isn't present. Fetch it first." >&2
  exit 1
fi

if ! git rev-parse --verify "$COMMIT^{commit}" >/dev/null 2>&1; then
  echo "FAIL (closed): '$COMMIT' is not a commit this repository knows about." >&2
  exit 1
fi

if git merge-base --is-ancestor "$COMMIT" "$MAIN_REF"; then
  echo "OK: $COMMIT is reachable from $MAIN_REF."
  exit 0
else
  echo "FAIL: $COMMIT is NOT reachable from $MAIN_REF -- refusing to release a tag that doesn't point to a commit that actually reached main." >&2
  exit 1
fi
