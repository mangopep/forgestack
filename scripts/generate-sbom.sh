#!/usr/bin/env bash
# Generates CycloneDX SBOMs for every ForgeStack artifact, locally or in CI.
# Requires: syft (https://github.com/anchore/syft). On macOS: `brew install syft`.
#
# Usage: scripts/generate-sbom.sh [output-dir]
set -euo pipefail

out_dir="${1:-sboms}"
mkdir -p "$out_dir"

if ! command -v syft >/dev/null 2>&1; then
  echo "error: syft is not installed. Install it first (e.g. 'brew install syft')." >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

echo "Generating Go module SBOM -> $out_dir/sbom-go.cdx.json"
syft dir:. --exclude './website/**' --exclude './apps/**' --exclude './sboms/**' \
  -o "cyclonedx-json=$out_dir/sbom-go.cdx.json"

if [ -n "${SKIP_IMAGE_SBOMS:-}" ]; then
  echo "SKIP_IMAGE_SBOMS set; skipping container image SBOMs (requires Docker)."
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found; skipping container image SBOMs." >&2
  exit 0
fi

echo "Building Go API image for SBOM scanning..."
docker build -q -t forgestack:sbom-scan . >/dev/null
echo "Generating Go image SBOM -> $out_dir/sbom-go-image.cdx.json"
syft forgestack:sbom-scan -o "cyclonedx-json=$out_dir/sbom-go-image.cdx.json"

echo "Building Python API image for SBOM scanning..."
docker build -q -t forgestack-python-api:sbom-scan apps/python-api >/dev/null
echo "Generating Python image SBOM -> $out_dir/sbom-python-image.cdx.json"
syft forgestack-python-api:sbom-scan -o "cyclonedx-json=$out_dir/sbom-python-image.cdx.json"

echo "Done. SBOMs written to $out_dir/."
echo "Verify a signature with: cosign verify-blob --key <pub key> --bundle <sbom>.bundle --insecure-ignore-tlog <sbom>"
