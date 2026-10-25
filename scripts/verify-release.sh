#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

for file in \
  onoven/deploy_onoven.sh \
  installer/liveiso/prepare_assets.sh \
  installer/liveiso/build_live_iso.sh \
  installer/liveiso/build.sh \
  onoven/june_reaper.sh.junelocal \
  onoven/june_bootclear.sh.onoven; do
  if grep -q $'\r' "$file"; then
    echo "CRLF line endings in $file; the oven/live scripts must stay LF-only." >&2
    echo "Normalize the file (sed -i 's/\\r$//' $file) before releasing." >&2
    exit 1
  fi
done

for file in \
  onoven/deploy_onoven.sh \
  installer/liveiso/prepare_assets.sh \
  installer/liveiso/build_live_iso.sh \
  installer/liveiso/build.sh; do
  bash -n "$file"
done

for file in onoven/june_reaper.sh.junelocal onoven/june_bootclear.sh.onoven; do
  sh -n "$file"
done

# Every APK this release would bake must be internally consistent: resources.arsc
# may not name resource files the zip lacks.  v0.9.0 shipped a Chef APK that did
# (it was rebuilt on a case-insensitive filesystem, which collapsed the 35
# resource files whose names differ only by case), and every oven it was
# installed on boot-looped.  This gate is why that cannot ship again.
if [ -d installer/liveiso/assets ]; then
  shopt -s nullglob
  baked_apks=(installer/liveiso/assets/*.apk)
  shopt -u nullglob
  if [ "${#baked_apks[@]}" -gt 0 ]; then
    python3 scripts/check_apk_resources.py "${baked_apks[@]}"
  fi
fi

cd golocal
unformatted="$(gofmt -l \
  main.go rest.go status.go updater.go updater_test.go \
  internal/update/*.go cmd/june-update-publisher/*.go cmd/june-wireless-update/*.go)"
if [ -n "$unformatted" ]; then
  echo "Go files need gofmt:" >&2
  printf '%s\n' "$unformatted" >&2
  exit 1
fi

go test ./...
go vet ./...
make tools >/dev/null

if command -v node >/dev/null 2>&1; then
  js_tmp="$(mktemp --suffix=.js)"
  trap 'rm -f "$js_tmp"' EXIT
  sed -n '/^<script>$/,/^<\/script>$/p' local_page.html | sed '1d;$d' > "$js_tmp"
  node --check "$js_tmp"
fi

echo "release verification passed"
