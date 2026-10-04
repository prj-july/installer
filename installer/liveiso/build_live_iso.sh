#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

OUT="$(pwd)/june-local-live.iso"
DOCKER="$(command -v docker || command -v podman || true)"
if [ -z "$DOCKER" ]; then
  echo "docker or podman is required to build the live ISO." >&2
  exit 1
fi

# Bake a build id into the image so the running installer can tell the user
# exactly which build they booted (this prevents stale-USB confusion).
BUILD_ID="$(date +%Y%m%d-%H%M)-$( (cat build.sh prepare_assets.sh gui/server.py gui/web/*.html gui/web/*.css gui/web/*.js 2>/dev/null || true) | sha256sum | cut -c1-8)"
if [ -d assets ]; then
  printf '%s\n' "$BUILD_ID" > assets/BUILD_ID
else
  echo "warning: assets/ does not exist yet; run ./prepare_assets.sh first" >&2
fi
echo "Build id: $BUILD_ID"

"$DOCKER" build -t june-local-liveiso .
BUILD_CONTAINER="june-local-liveiso-build"
"$DOCKER" rm -f "$BUILD_CONTAINER" >/dev/null 2>&1 || true
"$DOCKER" run --privileged --name "$BUILD_CONTAINER" june-local-liveiso /build/build.sh
"$DOCKER" cp "$BUILD_CONTAINER:/build/live/live-image-amd64.hybrid.iso" "$OUT"
"$DOCKER" rm -f "$BUILD_CONTAINER" >/dev/null 2>&1 || true
echo "ISO written to $OUT"
