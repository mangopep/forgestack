#!/usr/bin/env bash
# Generates a new, standalone project from a ForgeStack service template.
# The generated project does NOT depend on this repository in any way --
# it is copied, has every placeholder substituted, and can be moved
# anywhere (including deleting this repository entirely) without breaking.
#
# Usage:
#   scripts/new-service.sh <go|python> <output-dir> --name <service-name> --port <port> \
#     [--module <go-module-path>] [--features base,docker,ci] [--validate]
#
# --features defaults to "base,docker,ci" (everything). Pass a subset to
# skip capabilities you don't want yet -- e.g. --features base to get only
# the application code, no Dockerfile/CI.
set -euo pipefail

usage() {
  echo "usage: $0 <go|python> <output-dir> --name <service-name> --port <port> [--module <path>] [--features base,docker,ci] [--validate]" >&2
  exit 2
}

[ $# -ge 2 ] || usage
LANGUAGE="$1"; shift
OUTPUT_DIR="$1"; shift

SERVICE_NAME=""
PORT=""
MODULE_PATH=""
FEATURES="base,docker,ci"
VALIDATE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --name) SERVICE_NAME="$2"; shift 2 ;;
    --port) PORT="$2"; shift 2 ;;
    --module) MODULE_PATH="$2"; shift 2 ;;
    --features) FEATURES="$2"; shift 2 ;;
    --validate) VALIDATE=1; shift ;;
    *) echo "unknown argument: $1" >&2; usage ;;
  esac
done

case "$LANGUAGE" in
  go|python) ;;
  *) echo "error: language must be 'go' or 'python', got '$LANGUAGE'" >&2; exit 1 ;;
esac

[ -n "$SERVICE_NAME" ] || { echo "error: --name is required" >&2; exit 1; }
if ! [[ "$SERVICE_NAME" =~ ^[a-z][a-z0-9-]{1,38}[a-z0-9]$ ]]; then
  echo "error: --name must be lowercase alphanumeric with hyphens, 3-40 chars, start with a letter (got '$SERVICE_NAME')" >&2
  exit 1
fi

[ -n "$PORT" ] || { echo "error: --port is required" >&2; exit 1; }
if ! [[ "$PORT" =~ ^[0-9]+$ ]] || [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
  echo "error: --port must be an integer 1-65535 (got '$PORT')" >&2
  exit 1
fi

if [ "$LANGUAGE" = "go" ]; then
  [ -n "$MODULE_PATH" ] || { echo "error: --module is required for a Go service (e.g. github.com/you/widgets)" >&2; exit 1; }
  if ! [[ "$MODULE_PATH" =~ ^[A-Za-z0-9_./-]+$ ]]; then
    echo "error: --module contains characters that aren't valid in a Go module path: '$MODULE_PATH'" >&2
    exit 1
  fi
fi

if [ -e "$OUTPUT_DIR" ]; then
  if [ -n "$(ls -A "$OUTPUT_DIR" 2>/dev/null)" ]; then
    echo "error: $OUTPUT_DIR already exists and is not empty -- refusing to overwrite" >&2
    exit 1
  fi
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
template_dir="$repo_root/templates/${LANGUAGE}-service"
[ -d "$template_dir" ] || { echo "error: template not found: $template_dir" >&2; exit 1; }

mkdir -p "$OUTPUT_DIR"

echo "Generating $LANGUAGE service '$SERVICE_NAME' into $OUTPUT_DIR (features: $FEATURES)..."

# Always-included base files.
if [ "$LANGUAGE" = "go" ]; then
  cp -r "$template_dir"/go.mod "$template_dir"/cmd "$template_dir"/internal "$template_dir"/README.md "$template_dir"/.gitignore "$template_dir"/template.json "$OUTPUT_DIR"/
else
  cp -r "$template_dir"/app "$template_dir"/tests "$template_dir"/pyproject.toml "$template_dir"/requirements-lock.txt "$template_dir"/README.md "$template_dir"/template.json "$OUTPUT_DIR"/
fi

IFS=',' read -ra FEATURE_LIST <<< "$FEATURES"
has_feature() {
  local want="$1"
  for f in "${FEATURE_LIST[@]}"; do
    [ "$f" = "$want" ] && return 0
  done
  return 1
}

if has_feature docker; then
  cp "$template_dir/Dockerfile" "$OUTPUT_DIR/Dockerfile"
  [ -f "$template_dir/compose.yaml" ] && cp "$template_dir/compose.yaml" "$OUTPUT_DIR/compose.yaml"
  [ -f "$template_dir/.dockerignore" ] && cp "$template_dir/.dockerignore" "$OUTPUT_DIR/.dockerignore"
fi

if has_feature ci; then
  mkdir -p "$OUTPUT_DIR/.github/workflows"
  cp "$template_dir/.github/workflows/ci.yml" "$OUTPUT_DIR/.github/workflows/ci.yml"
fi

# Substitute placeholders in every text file (skip binaries, there are none
# in these templates, but this guards against future additions).
find "$OUTPUT_DIR" -type f -print0 | while IFS= read -r -d '' f; do
  if file "$f" | grep -q text; then
    sed -i '' \
      -e "s|__SERVICE_NAME__|${SERVICE_NAME}|g" \
      -e "s|__PORT__|${PORT}|g" \
      $( [ "$LANGUAGE" = "go" ] && echo "-e s|__MODULE_PATH__|${MODULE_PATH}|g" ) \
      "$f"
  fi
done

# Record provenance (not required to run the generated project; purely
# informational, see docs/VERSIONING.md).
python3 -I -c "
import json, datetime
with open('$OUTPUT_DIR/template.json') as fh:
    meta = json.load(fh)
meta['generated_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
meta['generated_service_name'] = '$SERVICE_NAME'
meta['generated_features'] = '$FEATURES'.split(',')
with open('$OUTPUT_DIR/template.json', 'w') as fh:
    json.dump(meta, fh, indent=2)
    fh.write('\n')
"

echo "Generated. See $OUTPUT_DIR/README.md to run it."

if [ "$VALIDATE" -eq 1 ]; then
  echo "Validating generated project independently..."
  if [ "$LANGUAGE" = "go" ]; then
    ( cd "$OUTPUT_DIR" && go build ./... && go vet ./... && go test ./... )
  else
    ( cd "$OUTPUT_DIR" && python3 -m venv .venv && . .venv/bin/activate && pip install --quiet -r requirements-lock.txt pytest==9.0.3 httpx==0.28.1 && python -m pytest -q )
  fi
  echo "Validation passed."
fi
