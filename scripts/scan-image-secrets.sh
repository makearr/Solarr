#!/usr/bin/env bash
# Fails if any private value from private/secrets.md appears in an image filesystem.
# Usage: scan-image-secrets.sh <rootfs.tar | image-ref>. Run by `make image` before every push.
set -euo pipefail

SECRETS="private/secrets.md"
target="${1:?usage: scan-image-secrets.sh <rootfs.tar | image-ref>}"
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT

[ -f "$SECRETS" ] || { echo "scan: $SECRETS missing, nothing to scan for" >&2; exit 1; }

# Key-shaped values (not a lowercase identifier, path, command or file name) and host IPv4 addresses.
# A row marked "shippable" is compiled in on purpose (the TVDB project key) and skipped.
python3 -I - "$SECRETS" > "$work/patterns" <<'PY'
import re, sys
text = open(sys.argv[1]).read()
text = "\n".join(l for l in text.splitlines() if "shippable" not in l)
values = {ip for ip in re.findall(r'\b10\.\d{1,3}\.\d{1,3}\.\d{1,3}\b', text) if not ip.endswith(".0")}
for v in re.findall(r'`([^`\n]+)`', text):
    if (len(v) >= 10 and not re.fullmatch(r'[a-z_.:-]+', v) and not re.search(r'[\s/$()<>|]', v)
            and not re.search(r'\.[a-z]{2,4}$', v)):
        values.add(v)
print("\n".join(sorted(values)))
PY
[ -s "$work/patterns" ] || { echo "scan: no patterns extracted from $SECRETS" >&2; exit 1; }

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
