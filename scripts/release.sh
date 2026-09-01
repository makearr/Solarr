#!/usr/bin/env bash
# Post-build release steps: bump, changelog, commit, tag, push, GitHub release.
# Invoked by `make image` only after a successful multi-arch build and push.
set -euo pipefail

REPO="makearr/MediaNexus"
REMOTE="github.com/${REPO}.git"
CHANGELOG="CHANGELOG.md"

die() { echo "release: $*" >&2; exit 1; }

token() {
  if [ -n "${GITHUB_TOKEN:-}" ]; then printf '%s' "$GITHUB_TOKEN"; return; fi
  grep -oP 'ghp_[A-Za-z0-9]+' private/secrets.md 2>/dev/null | head -1 ||
    die "no token: set GITHUB_TOKEN or add it to private/secrets.md"
}

prev_version=$(tr -d '[:space:]' < VERSION)
prev_tag=$(git tag -l 'v*' --sort=-v:refname | head -1)

# ── bump BUILD ────────────────────────────────────────────────────────────────
new_version=$(awk -F. '{printf "%s.%s.%d", $1, $2, $3+1}' VERSION)
echo "$new_version" > VERSION
tag="v${new_version}"

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
  printf '# Changelog\n\nBuild numbers advance on each successful image build; see\n`documentation/versioning.md`.\n\n%s\n' "$entry" > "$CHANGELOG"
fi

# ── commit, tag, push ─────────────────────────────────────────────────────────
git add VERSION "$CHANGELOG"
git commit -q -m "Build ${new_version}

Image ghcr.io/makearr/medianexus:${new_version} built and pushed for
linux/amd64 and linux/arm64 from $(git rev-parse --short HEAD)."

git tag -a "$tag" -m "Build ${new_version}

${subjects}"

TOKEN=$(token)
git push -q "https://x-access-token:${TOKEN}@${REMOTE}" HEAD:main "$tag" ||
  die "git push failed — VERSION is already ${new_version} locally; re-run push manually"

# ── GitHub release ────────────────────────────────────────────────────────────
body=$(printf '%s\n\n---\n\nImage: `ghcr.io/makearr/medianexus:%s`\nPlatforms: linux/amd64, linux/arm64' "$subjects" "$new_version")
code=$(python3 - "$tag" "$new_version" "$body" <<'PY'
import json, os, sys, urllib.request, re
tag, ver, body = sys.argv[1], sys.argv[2], sys.argv[3]
tok = re.search(r'ghp_[A-Za-z0-9]+', open('private/secrets.md').read()).group(0) \
      if not os.environ.get('GITHUB_TOKEN') else os.environ['GITHUB_TOKEN']
payload = json.dumps({"tag_name": tag, "name": f"Build {ver}", "body": body,
                      "draft": False, "prerelease": ver.startswith("0.")}).encode()
req = urllib.request.Request("https://api.github.com/repos/makearr/MediaNexus/releases",
        data=payload, method="POST",
        headers={"Authorization": f"Bearer {tok}", "Accept": "application/vnd.github+json"})
try:
    with urllib.request.urlopen(req) as r: print(r.status)
except Exception as e: print(getattr(e, "code", "error"))
PY
)
[ "$code" = "201" ] && echo "release: published $tag" || echo "release: tag pushed, GitHub release returned $code"
echo "release: ${prev_version} -> ${new_version}"
