#!/usr/bin/env bash
# Fails if any private value from private/secrets.md appears in an image filesystem.
# Usage: scan-image-secrets.sh <rootfs.tar | image-ref>. Run by `make image` before every push.
set -euo pipefail

target="${1:?usage: scan-image-secrets.sh <rootfs.tar | image-ref>}"
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT

scripts/private-patterns.sh > "$work/patterns"
[ -s "$work/patterns" ] || { echo "scan: no patterns extracted from private/secrets.md" >&2; exit 1; }

if [ -f "$target" ]; then
  tarball="$target"
else
  cid=$(docker create "$target")
  docker export "$cid" > "$work/rootfs.tar"; docker rm -f "$cid" >/dev/null
  tarball="$work/rootfs.tar"
fi
docker image inspect "$target" --format '{{json .Config}}' > "$work/config" 2>/dev/null || :

mkdir "$work/fs"; tar -xf "$tarball" -C "$work/fs" 2>/dev/null || :
# -l lists the offending files; the matched value itself is never printed.
hits=$(grep -rlaF -f "$work/patterns" "$work/fs" "$work/config" 2>/dev/null | sed "s#^$work/fs##" || :)
if [ -n "$hits" ]; then
  echo "scan: FAILED, private values found in $target:" >&2; echo "$hits" >&2; exit 1
fi
echo "scan: clean ($(wc -l < "$work/patterns") private values checked, $target)"
