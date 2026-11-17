#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT INT TERM

"$repo_root/scripts/new-service.sh" go "$test_root/one" --name sample-api --port 18081 --module example.com/sample-api --features base,docker,ci --validate
"$repo_root/scripts/new-service.sh" go "$test_root/two" --name sample-api --port 18081 --module example.com/sample-api --features base,docker,ci

hash_tree() {
  FORGESTACK_TREE="$1" python3 -I -c '
import hashlib, os
root=os.environ["FORGESTACK_TREE"]
h=hashlib.sha256()
paths=[]
for parent, _, names in os.walk(root):
    paths.extend(os.path.join(parent, name) for name in names)
for path in sorted(paths):
    rel=os.path.relpath(path, root).encode()
    h.update(len(rel).to_bytes(8, "big")); h.update(rel)
    with open(path, "rb") as source: h.update(source.read())
print(h.hexdigest())
'
}
test "$(hash_tree "$test_root/one")" = "$(hash_tree "$test_root/two")"
test -f "$test_root/one/Dockerfile"
test -f "$test_root/one/compose.yaml"
grep -q 'generated_features' "$test_root/one/template.json"

if "$repo_root/scripts/new-service.sh" go "$test_root/rejected" --name sample-api --port 18081 --module example.com/sample-api --features base,unsupported >"$test_root/out" 2>&1; then
  echo "expected unsupported feature to fail" >&2
  exit 1
fi
test ! -e "$test_root/rejected"
test -z "$(find "$test_root" -maxdepth 1 -name '.rejected.forgestack.*' -print -quit)"
echo "template generation determinism, validation, and failed-generation cleanup passed"
