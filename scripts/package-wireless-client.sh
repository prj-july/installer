#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/dist/june-wireless-update"
BIN="$ROOT/golocal/bin"

for file in \
  june-wireless-update-linux-amd64 \
  june-wireless-update-windows-amd64.exe; do
  if [ ! -f "$BIN/$file" ]; then
    echo "missing $BIN/$file; run 'make client-linux client-windows'" >&2
    exit 1
  fi
done

mkdir -p "$OUT"
cp "$BIN/june-wireless-update-linux-amd64" "$OUT/"
cp "$BIN/june-wireless-update-windows-amd64.exe" "$OUT/"
cp "$ROOT/packaging/wireless-client/README.txt" "$OUT/"
(
  cd "$OUT"
  sha256sum june-wireless-update-linux-amd64 \
    june-wireless-update-windows-amd64.exe > SHA256SUMS
)

echo "packaged wireless updater client in $OUT"
