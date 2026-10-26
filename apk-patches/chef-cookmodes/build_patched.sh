#!/usr/bin/env bash
# Build Chef_cookmodes_updates_signed.apk from the stock Chef APK.
#
# Usage:
#   ./build_patched.sh [stock-Chef.apk] [output.apk]
#
# Defaults:
#   stock  <june-root>/local-resources/june-rcm/healthy-build/decompiled/priv-app-apks/Chef.apk
#   output ./Chef_cookmodes_updates_signed.apk
#
# Decode, patch, rebuild and sign all happen in a temp dir under /tmp, and only
# the final signed APK is copied back. Never run the apktool build on the SMB
# share: the 0.9.0 boot loop came from rebuilding on a case-insensitive
# filesystem, which silently collapsed the resource files whose names differ
# only by case. Requires apktool, python3, java/jarsigner and the project APK
# key.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# This repo keeps apk-patches/ directly under the June root
# (<root>/installer/apk-patches/chef-cookmodes), while the older layout had it
# at <root>/apk-patches/chef-cookmodes. Walk up until tools/certs is found so
# the documented command works from either.
JUNE_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
while [ ! -f "$JUNE_ROOT/tools/certs/june_apk_sign.p12" ] && [ "$JUNE_ROOT" != "/" ]; do
  JUNE_ROOT="$(cd "$JUNE_ROOT/.." && pwd)"
done

STOCK="${1:-$JUNE_ROOT/local-resources/june-rcm/healthy-build/decompiled/priv-app-apks/Chef.apk}"
OUT="${2:-$SCRIPT_DIR/Chef_cookmodes_updates_signed.apk}"

APKTOOL="${APKTOOL:-apktool}"
KS="${KS:-$JUNE_ROOT/tools/certs/june_apk_sign.p12}"
KS_PASS="${KS_PASS:-june}"
KS_ALIAS="${KS_ALIAS:-june}"

[ -f "$STOCK" ] || { echo "stock APK not found: $STOCK" >&2; exit 1; }
command -v "$APKTOOL" >/dev/null 2>&1 || { echo "apktool not found: $APKTOOL" >&2; exit 1; }
[ -f "$KS" ] || { echo "signing keystore not found: $KS" >&2; exit 1; }

WORK="$(mktemp -d /tmp/chef-cookmodes.XXXXXX)"
cleanup() {
  case "$WORK" in
    /tmp/chef-cookmodes.*) rm -rf -- "$WORK" ;;
  esac
}
trap cleanup EXIT

echo "[*] stock:  $STOCK"
echo "[*] work:   $WORK"
echo "[*] decode (resources kept raw)"
if ! "$APKTOOL" d -r -f -o "$WORK/decoded" "$STOCK" > "$WORK/decode.log" 2>&1; then
  echo "[-] apktool d failed:" >&2
  cat "$WORK/decode.log" >&2
  exit 1
fi

echo "[*] patch smali"
python3 "$SCRIPT_DIR/patch.py" "$WORK/decoded"

echo "[*] rebuild"
if ! "$APKTOOL" b "$WORK/decoded" -o "$WORK/Chef_cookmodes_unsigned.apk" > "$WORK/build.log" 2>&1; then
  echo "[-] apktool b failed:" >&2
  cat "$WORK/build.log" >&2
  exit 1
fi
echo "[*] build log tail:"
tail -n 20 "$WORK/build.log"

echo "[*] sign with $KS_ALIAS"
# Sign in /tmp, then copy only the final signed APK back: jarsigner rewrites
# the zip, and keeping that off the SMB share is part of the 0.9.0 lesson.
cp "$WORK/Chef_cookmodes_unsigned.apk" "$WORK/Chef_cookmodes_updates_signed.apk"
jarsigner -keystore "$KS" -storetype PKCS12 -storepass "$KS_PASS" \
  -sigalg SHA256withRSA -digestalg SHA-256 \
  "$WORK/Chef_cookmodes_updates_signed.apk" "$KS_ALIAS"
cp "$WORK/Chef_cookmodes_updates_signed.apk" "$OUT"

keytool -printcert -jarfile "$OUT" 2>/dev/null | grep -m1 Owner || true
stat -c '%n %s bytes' "$OUT"
sha256sum "$OUT"
echo "[+] wrote $OUT"
