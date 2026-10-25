#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

for file in \
  onoven/deploy_onoven.sh \
  installer/liveiso/prepare_assets.sh \
  installer/liveiso/build_live_iso.sh \
  installer/liveiso/build.sh; do
  bash -n <(tr -d '\r' < "$file")
done

for file in onoven/june_reaper.sh.junelocal onoven/june_bootclear.sh.onoven; do
  sh -n <(tr -d '\r' < "$file")
done

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
