#!/usr/bin/env bash
# Prints, one per line, the private values of $SECRETS (default private/secrets.md) that must never be published.
set -euo pipefail

SECRETS="${SECRETS:-private/secrets.md}"
[ -f "$SECRETS" ] || { echo "patterns: $SECRETS missing, nothing to scan for" >&2; exit 1; }

# Key-shaped values (not a lowercase identifier, path, command or file name) and host IPv4 addresses.
# A row marked "shippable" is compiled in on purpose (the TVDB project key) and skipped.
python3 -I - "$SECRETS" <<'PY'
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
