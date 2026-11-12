#!/usr/bin/env bash
set -u
for cmd in uname git go docker aws terraform kubectl make curl; do
  printf '%-12s' "$cmd"
  if command -v "$cmd" >/dev/null 2>&1; then command -v "$cmd"; else printf 'not installed\n'; fi
done
printf 'ARCH: '; uname -m
printf 'OS: '; uname -s
