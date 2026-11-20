#!/usr/bin/env bash
# Fails when a file holds a private value from private/secrets.md; prints the line numbers, never the value.
# Usage: scan-text-private.sh <file>. Run by release.sh on the text it publishes.
set -euo pipefail

file="${1:?usage: scan-text-private.sh <file>}"
patterns=$(mktemp); trap 'rm -f "$patterns"' EXIT
"$(dirname "$0")/private-patterns.sh" > "$patterns"
[ -s "$patterns" ] || { echo "scan: no patterns extracted" >&2; exit 1; }

if hits=$(grep -nF -f "$patterns" "$file" | cut -d: -f1 | paste -sd, -) && [ -n "$hits" ]; then
  echo "scan: FAILED, private values on line(s) $hits of $file" >&2; exit 1
fi
