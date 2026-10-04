#!/usr/bin/env bash
# Run the GUI backend in mock mode for UI development:
#   bash installer/liveiso/gui/dev/run-mock.sh        (then open http://localhost:8765/)
# Open it from another device (or via this machine's LAN/WSL address) to see
# the phone view; turn on "Connect a phone" in the local view first.
# Optional: a qrencode binary on PATH renders the QR codes.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
# A user-local qrencode (e.g. extracted with apt-get download) is picked up too.
if [ -x "$HOME/.cache/june-dev/x/usr/bin/qrencode" ]; then
  export PATH="$HOME/.cache/june-dev/x/usr/bin:$PATH"
  export LD_LIBRARY_PATH="$HOME/.cache/june-dev/x/usr/lib/x86_64-linux-gnu${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
fi
exec python3 "$here/../server.py" --mock --bind 0.0.0.0 --port "${PORT:-8765}" \
  --payload "$here/payload" "$@"
