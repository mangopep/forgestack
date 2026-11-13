#!/usr/bin/env bash
# Probes whether the current registry credentials can actually push to a
# given GHCR repository path, WITHOUT publishing anything retrievable: it
# initiates a blob-upload session (POST .../blobs/uploads/) and
# immediately cancels it (DELETE the upload session). No manifest, no
# tag, no blob ever becomes visible or pullable -- only the upload
# session's grant/denial is observed.
#
# This exists because v0.1.0's real release failure was exactly this
# class of problem (a registry-level "denied" on the second, differently-
# named image) discovered only after the build/scan/sign machinery had
# already run -- catching it this early, before any image is even built,
# is strictly better.
#
# NOT YET VERIFIED against a live failure case -- this was written and
# reasoned through from the Docker Registry HTTP API v2 spec, not proven
# against GHCR's actual current behavior for this repository (doing so
# would require a real registry push attempt, which this investigation
# was explicitly not authorized to make). Treat a PASS from this script
# as "no obvious permission problem detected," not an ironclad guarantee
# -- validate it for real the next time a release is actually attempted.
#
# Usage: preflight-registry-push-access.sh <registry-repo-path> <username> <password>
#   e.g. preflight-registry-push-access.sh mangopep/forgestack-python-api mangopep "$GITHUB_TOKEN"
set -euo pipefail

usage() {
  echo "usage: $0 <registry-repo-path> <username> <password>" >&2
  exit 2
}

[ $# -eq 3 ] || usage
REPO_PATH="$1"
USERNAME="$2"
PASSWORD="$3"
REGISTRY="ghcr.io"

# Step 1: GHCR challenges with a Bearer realm/service/scope via a 401 on
# an unauthenticated request -- this is standard Docker Registry v2
# Bearer-auth negotiation, not GHCR-specific.
challenge="$(curl -s -D - -o /dev/null "https://${REGISTRY}/v2/${REPO_PATH}/blobs/uploads/" -X POST || true)"
www_auth="$(printf '%s' "$challenge" | grep -i '^www-authenticate:' | head -1)"

if [ -z "$www_auth" ]; then
  echo "FAIL: no WWW-Authenticate challenge from $REGISTRY for $REPO_PATH -- cannot negotiate a token. Registry may be unreachable or behaving unexpectedly." >&2
  exit 1
fi

realm="$(printf '%s' "$www_auth" | grep -o 'realm="[^"]*"' | cut -d'"' -f2)"
service="$(printf '%s' "$www_auth" | grep -o 'service="[^"]*"' | cut -d'"' -f2)"

if [ -z "$realm" ] || [ -z "$service" ]; then
  echo "FAIL: could not parse realm/service from challenge: $www_auth" >&2
  exit 1
fi

# Step 2: exchange the username/password for a scoped bearer token with
# push permission explicitly requested.
token_response="$(curl -s -u "${USERNAME}:${PASSWORD}" \
  "${realm}?service=${service}&scope=repository:${REPO_PATH}:push,pull")"
token="$(printf '%s' "$token_response" | python3 -I -c "import json,sys; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || true)"

if [ -z "$token" ]; then
  echo "FAIL: did not receive a bearer token for push,pull on $REPO_PATH -- credentials rejected at the token-exchange step (before any content was touched)." >&2
  echo "token response: $token_response" >&2
  exit 1
fi

# Step 3: actually attempt (and immediately cancel) a blob-upload
# session -- the only reliable way to confirm push is actually granted,
# not just that pull is. This creates no retrievable artifact.
upload_response="$(curl -s -D - -o /dev/null -X POST \
  -H "Authorization: Bearer ${token}" \
  "https://${REGISTRY}/v2/${REPO_PATH}/blobs/uploads/")"
upload_status="$(printf '%s' "$upload_response" | head -1 | grep -o '[0-9][0-9][0-9]')"
location="$(printf '%s' "$upload_response" | grep -i '^location:' | head -1 | tr -d '\r' | sed 's/^[Ll]ocation: *//')"

if [ "$upload_status" != "202" ]; then
  echo "FAIL: blob-upload session for $REPO_PATH returned HTTP $upload_status, expected 202 Accepted -- push is not actually granted for this exact repository path, even though login/token-exchange succeeded." >&2
  exit 1
fi

# Cancel the session immediately -- nothing was uploaded, this just
# releases the server-side session so it isn't left dangling.
if [ -n "$location" ]; then
  curl -s -o /dev/null -X DELETE -H "Authorization: Bearer ${token}" "$location" || true
fi

echo "OK: push access confirmed for $REPO_PATH (blob-upload session granted and cancelled, nothing published)."
