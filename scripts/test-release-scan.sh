#!/usr/bin/env bash
# Checks that scan-text-private.sh refuses text holding a private value and passes clean text.
set -euo pipefail

dir=$(mktemp -d); trap 'rm -rf "$dir"' EXIT
printf '%s\n' '- host `10.99.99.99`' > "$dir/secrets.md"
printf '%s\n' '- Fix the matcher' 'Reached 10.99.99.99 today' > "$dir/dirty.txt"
printf '%s\n' '- Fix the matcher' > "$dir/clean.txt"

SECRETS="$dir/secrets.md" "$(dirname "$0")/scan-text-private.sh" "$dir/clean.txt"
if SECRETS="$dir/secrets.md" "$(dirname "$0")/scan-text-private.sh" "$dir/dirty.txt" 2>"$dir/err"; then
  echo "FAIL: a subject holding a private address was accepted" >&2; exit 1
fi
grep -q 'line(s) 2 ' "$dir/err" || { echo "FAIL: line number not reported" >&2; cat "$dir/err" >&2; exit 1; }
if grep -q '10.99.99.99' "$dir/err"; then echo "FAIL: the value was printed" >&2; exit 1; fi
echo "release scan: ok"
