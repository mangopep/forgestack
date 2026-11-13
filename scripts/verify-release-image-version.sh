#!/usr/bin/env bash
# Runs a built image, hits /healthz, and fails unless the reported
# "version" field matches the expected release version exactly. This is
# the check that would have caught v0.1.0's real bug: the image built
# without error, scanned clean, and ran fine -- it just silently reported
# "dev" instead of the real release version, because no build argument
# carried the version into the Dockerfile's build. A clean build and a
# clean Trivy scan say nothing about this; only actually running the
# image and reading its own reported version does.
#
# Usage: scripts/verify-release-image-version.sh <image:tag> <port> <expected-version>
set -euo pipefail

usage() {
  echo "usage: $0 <image:tag> <port> <expected-version>" >&2
  exit 2
}

[ $# -eq 3 ] || usage
IMAGE="$1"
PORT="$2"
EXPECTED_VERSION="$3"

container_name="verify-release-version-$$"
cleanup() { docker stop "$container_name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run --name "$container_name" -d -p "${PORT}:${PORT}" -e "PORT=${PORT}" "$IMAGE" >/dev/null

healthz_body=""
for _ in $(seq 1 20); do
  if healthz_body="$(curl -sf "http://127.0.0.1:${PORT}/healthz" 2>/dev/null)"; then
    break
  fi
  sleep 1
done

if [ -z "$healthz_body" ]; then
  echo "FAIL: $IMAGE never responded on /healthz within 20s -- cannot verify its version." >&2
  docker logs "$container_name" >&2 || true
  exit 1
fi

echo "healthz response: $healthz_body"

actual_version="$(python3 -I -c "import json,sys; print(json.loads(sys.argv[1]).get('version',''))" "$healthz_body")"

if [ "$actual_version" != "$EXPECTED_VERSION" ]; then
  echo "FAIL: $IMAGE reports version '$actual_version', expected '$EXPECTED_VERSION' -- the release's version build-arg did not reach the running binary." >&2
  exit 1
fi

echo "OK: $IMAGE correctly reports version '$actual_version'."
