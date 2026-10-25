#!/usr/bin/env bash
# Post-build release steps: bump, changelog, commit, tag, push, GitHub release.
# Invoked by `make image` only after a successful multi-arch build and push.
set -euo pipefail

REPO="makearr/Solarr"
REMOTE="github.com/${REPO}.git"
CHANGELOG="CHANGELOG.md"

die() { echo "release: $*" >&2; exit 1; }

token() {
  if [ -n "${GITHUB_TOKEN:-}" ]; then printf '%s' "$GITHUB_TOKEN"; return; fi
  grep -oP '(ghp_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+)' private/secrets.md 2>/dev/null | sed -n '1p' ||
    die "no token: set GITHUB_TOKEN or add it to private/secrets.md"
}

prev_version=$(tr -d '[:space:]' < VERSION)
prev_tag=$(git tag -l 'v*' --sort=-v:refname | head -1)

# ── the next version and milestone name are computed by the Makefile, which already
# built and pushed the image tagged with them — this script only records that fact ──
new_version="${NEXT_VERSION:?NEXT_VERSION must be set by the Makefile}"
milestone="${MILESTONE:-}"
echo "$new_version" > VERSION
tag="v${new_version}"
title="${new_version}"
[ -n "$milestone" ] && title="${new_version} — ${milestone}"

# ── changelog: commits since the previous tag ─────────────────────────────────
range=""; [ -n "$prev_tag" ] && range="${prev_tag}..HEAD"
subjects=$(git log $range --no-merges --pretty=format:'- %s' 2>/dev/null || true)
[ -z "$subjects" ] && subjects="- No source changes since ${prev_tag:-the initial commit}"

entry="## ${new_version} — $(date -u +%Y-%m-%d)

${subjects}
"
if [ -f "$CHANGELOG" ]; then
  # insert beneath the header, newest first
  { head -n 4 "$CHANGELOG"; echo; echo "$entry"; tail -n +5 "$CHANGELOG"; } > "$CHANGELOG.tmp"
  mv "$CHANGELOG.tmp" "$CHANGELOG"
else
  printf '# Changelog\n\nVersion is `YYYY.MM.RRR.BBB` (year.month.release.build); see\n`documentation/versioning.md`.\n\n%s\n' "$entry" > "$CHANGELOG"
fi

# ── commit, tag, push ─────────────────────────────────────────────────────────
git add VERSION "$CHANGELOG"
git commit -q -m "Release ${new_version}

Images ghcr.io/makearr/solarr and makearr/solarr (Docker Hub), tags
${new_version} and latest, built for linux/amd64 and linux/arm64 from $(git rev-parse --short HEAD)."

git tag -a "$tag" -m "Release ${title}

${subjects}"

TOKEN=$(token)
git push -q "https://x-access-token:${TOKEN}@${REMOTE}" HEAD:main "$tag" ||
  die "git push failed — VERSION is already ${new_version} locally; re-run push manually"

# ── GitHub release ────────────────────────────────────────────────────────────
body=$(printf '%s\n\n---\n\nImages: `makearr/solarr:%s` (Docker Hub), `ghcr.io/makearr/solarr:%s`\nPlatforms: linux/amd64, linux/arm64' "$subjects" "$new_version" "$new_version")
code=$(python3 - "$tag" "$title" "$body" <<'PY'
import json, os, sys, urllib.request, re
tag, title, body = sys.argv[1], sys.argv[2], sys.argv[3]
tok = re.search(r'(?:ghp_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+)', open('private/secrets.md').read()).group(0) \
      if not os.environ.get('GITHUB_TOKEN') else os.environ['GITHUB_TOKEN']
payload = json.dumps({"tag_name": tag, "name": title, "body": body,
                      "draft": False, "prerelease": False}).encode()
req = urllib.request.Request("https://api.github.com/repos/makearr/Solarr/releases",
        data=payload, method="POST",
        headers={"Authorization": f"Bearer {tok}", "Accept": "application/vnd.github+json"})
try:
    with urllib.request.urlopen(req) as r: print(r.status)
except Exception as e: print(getattr(e, "code", "error"))
PY
)
[ "$code" = "201" ] && echo "release: published $tag" || echo "release: tag pushed, GitHub release returned $code"
echo "release: ${prev_version} -> ${new_version}"
