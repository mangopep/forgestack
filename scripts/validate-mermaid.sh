#!/usr/bin/env bash
# Independently validates every ```mermaid code block under docs/ by
# actually parsing and rendering it with the real mermaid-cli (mmdc) —
# not just trusting that `docusaurus build` didn't error, since Mermaid
# diagrams render client-side only and a webpack build succeeding proves
# nothing about whether the diagram syntax itself is valid.
#
# Run from the repository root: scripts/validate-mermaid.sh
set -euo pipefail
cd "$(dirname "$0")/.."

OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

while IFS= read -r -d '' file; do
  awk -v outdir="$OUT" -v base="$(basename "$file" .md)" '
    /^```mermaid$/ { n++; capture=1; buf=""; next }
    /^```$/ && capture { capture=0; fname=outdir"/"base"-"n".mmd"; print buf > fname; close(fname); next }
    capture { buf = buf $0 "\n" }
  ' "$file"
done < <(find docs -name "*.md" -print0)

shopt -s nullglob
mmd_files=("$OUT"/*.mmd)
if [ ${#mmd_files[@]} -eq 0 ]; then
  echo "validate-mermaid: no \`\`\`mermaid blocks found under docs/ — nothing to validate"
  exit 0
fi

fail=0
for mmd in "${mmd_files[@]}"; do
  svg="${mmd%.mmd}.svg"
  if npx --yes @mermaid-js/mermaid-cli@12.2.0 \
      -p scripts/mermaid-puppeteer-config.json \
      -i "$mmd" -o "$svg" --quiet 2>"${mmd}.err"; then
    echo "OK   $(basename "$mmd")"
  else
    echo "FAIL $(basename "$mmd")"
    cat "${mmd}.err" >&2
    fail=$((fail + 1))
  fi
done

echo ""
echo "validate-mermaid: rendered ${#mmd_files[@]} diagram(s), $fail failed"
exit "$fail"
