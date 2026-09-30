#!/usr/bin/env bash
# One-shot end-to-end smoke run (revision.md 10.1). Builds the image, starts one
# throwaway instance, drives it with Playwright and removes the instance again --
# whether or not the checks pass, which is what makes it compatible with the
# no-standing-container rule by construction.
set -uo pipefail

PORT="${E2E_PORT:-15455}"
NAME="${E2E_CONTAINER:-solarr-e2e}"
IMAGE="${E2E_IMAGE:-ghcr.io/makearr/solarr:dev}"
PLAYWRIGHT_IMAGE="${E2E_PLAYWRIGHT_IMAGE:-mcr.microsoft.com/playwright:v1.55.1-noble}"
MUSIC="${E2E_MUSIC:-/storage/media/music}"
DOWNLOADS="${E2E_DOWNLOADS:-/storage/media/download}"
# A folder under DOWNLOADS the import check commits; machine-local (Makefile.local), never in git.
IMPORT_FOLDER="${E2E_IMPORT_FOLDER:-}"
if [ -z "$IMPORT_FOLDER" ] || [ ! -d "$DOWNLOADS/$IMPORT_FOLDER" ]; then
  echo "e2e: set E2E_IMPORT_FOLDER (in Makefile.local) to an identifiable album folder under $DOWNLOADS" >&2
  exit 2
fi
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/solarr-e2e.XXXXXX")"
OUT="$REPO/web/e2e/out"

# Removing the container is the one thing that must happen no matter what.
cleanup() {
  local status=$?
  docker rm -f "$NAME" >/dev/null 2>&1
  rm -rf "$WORK"
  if docker ps -a --format '{{.Names}}' | grep -qx "$NAME"; then
    echo "e2e: WARNING: container $NAME is still present" >&2
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

mkdir -p "$WORK/config" "$WORK/library" "$OUT"
rm -rf "${OUT:?}/report" "${OUT:?}/artifacts"

docker rm -f "$NAME" >/dev/null 2>&1
echo "e2e: starting $NAME on 127.0.0.1:$PORT"
docker run -d --name "$NAME" \
  -e PUID="$(id -u)" -e PGID="$(id -g)" \
  -v "$WORK/config":/config \
  -v "$MUSIC":/media/music:ro \
  -v "$DOWNLOADS":/media/download:ro \
  -v "$WORK/library":/library \
  -p "127.0.0.1:$PORT:5454" "$IMAGE" >/dev/null || exit 1

echo -n "e2e: waiting for /ready"
for _ in $(seq 1 60); do
  if [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/v1/ready")" = "200" ]; then
    echo " - ready"
    break
  fi
  echo -n "."
  sleep 1
done
if [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/v1/ready")" != "200" ]; then
  echo
  echo "e2e: the instance never became ready; its own log follows" >&2
  docker logs "$NAME" 2>&1 | tail -40 >&2
  exit 1
fi

# --network host so the suite reaches the published port; --ipc=host is
# Playwright's own recommendation for Chromium's shared memory.
docker run --rm --network host --ipc=host \
  -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -e SOLARR_URL="http://127.0.0.1:$PORT" \
  -e E2E_IMPORT_FIXTURE="/media/download/$IMPORT_FOLDER" \
  -e npm_config_cache=/tmp/.npm \
  -v solarr-npm:/tmp/.npm \
  -v "$REPO/web/e2e":/e2e -v "$OUT":/e2e-out -w /e2e \
  "$PLAYWRIGHT_IMAGE" sh -c 'npm ci --no-audit --no-fund && npx playwright test'
status=$?

if [ "$status" -ne 0 ]; then
  echo "e2e: FAILED. Screenshots and traces: $OUT/artifacts; report: $OUT/report" >&2
  docker logs "$NAME" 2>&1 | tail -40 >&2
fi
exit "$status"
