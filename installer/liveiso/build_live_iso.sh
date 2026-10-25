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
# Release version from the nearest v* tag: "v0.8" on the tagged commit,
# "v0.8-3-g9a0350c" for later test builds, "-dirty" with uncommitted changes.
# Tags may be v0.8 or 0.8; strip the leading v for display. JUNE_VERSION
# overrides the tag.
VERSION="${JUNE_VERSION:-$(git describe --tags --match 'v[0-9]*' --match '[0-9]*' --dirty 2>/dev/null || echo 0.8.1)}"
VERSION="${VERSION#v}"
# The payload binaries and the identity/mDNS payload are part of the identity: a
# rebuilt june-local or june-camerad, a different accounts template or identity
# generator, or a new JuneDns helper must produce a new build id even when the
# scripts do not change.  Missing files are skipped here (the JuneDns APK and the
# identity template are optional on dev builds; release builds fail earlier if
# the template is missing).
IDENTITY_ASSETS="assets/june-local-linux-armv7 assets/june-camerad-linux-armv7 assets/june-wireless-update assets/Chef_cookmodes_signed.apk assets/updater.json assets/generate_identity.py assets/accounts_template.db assets/JuneDns_signed.apk"
BUILD_ID="$(date +%Y%m%d-%H%M)-$( (cat build.sh prepare_assets.sh gui/server.py gui/web/*.html gui/web/*.css gui/web/*.js boot-theme/* 2>/dev/null || true; printf '%s' "$VERSION"; sha256sum $IDENTITY_ASSETS 2>/dev/null || true) | sha256sum | cut -c1-8)"
if [ -d assets ]; then
  printf '%s\n' "$BUILD_ID" > assets/BUILD_ID
  printf '%s\n' "$VERSION" > assets/VERSION
else
  echo "warning: assets/ does not exist yet; run ./prepare_assets.sh first" >&2
fi
echo "Version:  $VERSION"
echo "Build id: $BUILD_ID"

"$DOCKER" build -t june-local-liveiso .
BUILD_CONTAINER="june-local-liveiso-build"
"$DOCKER" rm -f "$BUILD_CONTAINER" >/dev/null 2>&1 || true
"$DOCKER" run --privileged --name "$BUILD_CONTAINER" june-local-liveiso /build/build.sh
"$DOCKER" cp "$BUILD_CONTAINER:/build/live/live-image-amd64.hybrid.iso" "$OUT"
"$DOCKER" rm -f "$BUILD_CONTAINER" >/dev/null 2>&1 || true

# Keep a versioned copy so old builds stay reviewable when a bug report comes
# in.  The canonical june-local-live.iso name is what Ventoy and the USB tool
# expect; this archive is for local history (the published R2 objects remain
# the canonical record for users).
ARCHIVE="$(pwd)/june-local-live-${VERSION}-${BUILD_ID}.iso"
cp -p "$OUT" "$ARCHIVE"
( cd "$(pwd)" && sha256sum "${ARCHIVE##*/}" > "${ARCHIVE##*/}.sha256" )

echo "ISO written to $OUT ($VERSION, build $BUILD_ID)"
echo "Archived as $ARCHIVE"
