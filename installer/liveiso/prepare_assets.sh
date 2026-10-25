#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Paths default to this checkout but can be pointed anywhere, which keeps the
# payload build usable from a published/source-only tree. See PUBLISHING.md.
if [ -z "${JUNE_LR:-}" ]; then
  if [ -d "$SCRIPT_DIR/../../../june-arch/onoven" ]; then
    # Development tree: <root>/local-resources/june-arch/installer/liveiso
    LR="$(cd "$SCRIPT_DIR/../../.." && pwd)"
  else
    # Flat installer repo: <repo>/installer/liveiso
    LR="$(cd "$SCRIPT_DIR/../.." && pwd)"
  fi
else
  LR="$JUNE_LR"
fi
ROOT="${JUNE_ROOT:-$(dirname "$LR")}"
# This repo is flat (liveiso/, onoven/, ...); the development tree nests the
# same directories under june-arch/.
if [ -d "$LR/june-arch/onoven" ]; then
  ARCH="$LR/june-arch"
else
  ARCH="$LR"
fi
# Onoven sources come from this repo, not the local-resources/june-arch dev
# tree (which can be stale and lacks the identity code). ARCH still supplies
# the built binaries/APKs/template from the local-resources tree.
ONOVEN_SRC="$ROOT/installer/onoven"
DEST="assets"
rm -rf "$DEST"
mkdir -p "$DEST"

# Stamp every generated script with the build time so a log pasted back from
# the live system can be matched to the exact ISO that produced it.
date -u +%Y%m%dT%H%M%SZ > "$DEST/BUILD_ID"
printf '%s\n' "${JUNE_VERSION:-0.8}" > "$DEST/VERSION"

cp "$ONOVEN_SRC/deploy_onoven.sh" "$DEST/"
cp "$ONOVEN_SRC/june_bootclear.sh.onoven" "$DEST/"
cp "$ONOVEN_SRC/june_reaper.sh.junelocal" "$DEST/"
cp "$ONOVEN_SRC/updater.json" "$DEST/"
# Guard against a stale onoven source tree.  A JUNE_LR pointing at the
# local-resources/june-arch dev tree picks up its old copies of these scripts,
# which silently ships an installer without the June identity code (no
# account pre-seed, no boot-time self-heal) even though prepare_assets itself
# is current.  Fail loudly instead: run prepare_assets.sh from the installer
# repo (`installer/` here), or point JUNE_LR at it.
if ! grep -q 'prepare_identity_assets' "$DEST/deploy_onoven.sh" || \
   ! grep -q 'ensure_june_identity' "$DEST/june_reaper.sh.junelocal"; then
  echo "[-] The onoven source tree at $ONOVEN_SRC is stale: the copied deploy_onoven.sh/" >&2
  echo "[-] june_reaper.sh.junelocal are missing the June identity code." >&2
  echo "[-] Run prepare_assets.sh from the installer repo (cd installer/installer/liveiso &&" >&2
  echo "[-] ./prepare_assets.sh); do not point JUNE_LR at local-resources/june-arch." >&2
  exit 1
fi
cp "$ARCH/companions_state.json" "$DEST/"
cp "$ARCH/golocal/bin/june-local-linux-armv7" "$DEST/"
cp "$ARCH/golocal/bin/june-camerad-linux-armv7" "$DEST/"
if [ ! -f "$ARCH/golocal/bin/june-wireless-update-linux-amd64" ]; then
  echo "[-] missing $ARCH/golocal/bin/june-wireless-update-linux-amd64" >&2
  echo "[-] run 'make client-linux' in $ARCH/golocal before preparing installer assets." >&2
  exit 1
fi
cp "$ARCH/golocal/bin/june-wireless-update-linux-amd64" "$DEST/june-wireless-update"
cp "$ROOT/tools/certs/junelife_chain_godaddy_intermediate.crt" "$DEST/"
cp "$ROOT/tools/certs/june-local.key" "$DEST/"
# The project CA alone (not the chain): the basic install adds it to the oven's
# trust store as a trust anchor and serves it at /local/ca.crt for the phone.
cp "$ROOT/tools/certs/junelife.crt" "$DEST/junelife_ca.crt"
cp "$LR/june-rcm/healthy-build/decrypted-priv-app/JuneCloudMessaging.apk" "$DEST/"
cp "$ARCH/apk-patches/jcm-pinning/JuneCloudMessaging_nopin_signed.apk" "$DEST/"
cp "$ARCH/apk-patches/remotes-pinning/Remotes_nopin_signed.apk" "$DEST/"
cp "$ARCH/apk-patches/mediacapturer-pinning/MediaCapturer_nopin_signed.apk" "$DEST/"
cp "$ARCH/apk-patches/analytics-pinning/Analytics_nopin_signed.apk" "$DEST/"
cp "$ARCH/apk-patches/companion-endpoint/companion_endpoint_signed.apk" "$DEST/"
cp "$ONOVEN_SRC/oven-light/OvenLight_signed.apk" "$DEST/"
# June Local DNS: a tiny helper app that advertises <hostname>.local over mDNS.
# The APK is built Arch-side (onoven/june-dns/build.sh, not committed), so it is
# copied into the payload from DNS_SRC or one of the default locations below.
# Optional: a payload without it still installs (the oven is reachable by IP,
# and a reinstall keeps any JuneDns already on the oven).
DNS_APK="${DNS_SRC:-}"
if [ -z "$DNS_APK" ]; then
  for candidate in \
    "$ARCH/onoven/june-dns/JuneDns_signed.apk" \
    "$ONOVEN_SRC/june-dns/JuneDns_signed.apk" \
    "$LR/onoven/june-dns/JuneDns_signed.apk"; do
    if [ -f "$candidate" ]; then
      DNS_APK="$candidate"
      break
    fi
  done
fi
if [ -n "$DNS_APK" ] && [ -f "$DNS_APK" ]; then
  cp "$DNS_APK" "$DEST/JuneDns_signed.apk"
  echo "[*] June Local DNS helper included from $DNS_APK ($(du -h "$DNS_APK" | cut -f1))"
else
  echo "[!] June Local DNS helper not found (set DNS_SRC=/path/to/JuneDns_signed.apk, or build it with onoven/june-dns/build.sh and copy it to $ARCH/onoven/june-dns/ or $ONOVEN_SRC/june-dns/); <name>.local resolution will be unavailable" >&2
fi
# Chef cook-modes patch (optional). Built from the user's own stock Chef APK
# with apk-patches/chef-cookmodes/build_patched.sh; June's assets are not
# published. Without it the oven keeps the stock primitive whitelist.
CHEF_APK="$ARCH/apk-patches/chef-cookmodes/Chef_cookmodes_signed.apk"
if [ -f "$CHEF_APK" ]; then
  cp "$CHEF_APK" "$DEST/"
  echo "[*] Chef cook-modes patch included: $(du -h "$CHEF_APK" | cut -f1)"
else
  echo "[!] Chef cook-modes patch not found at $CHEF_APK; toast/other remote modes stay stock" >&2
fi
# Presets/recipes content mirror (catalog + program bundles). June's content is
# licensed material: it is used here for personal/local installs only and is
# deliberately NOT published in the source repo (see PUBLISHING.md). Point
# JUNE_CONTENT_DIR elsewhere, or remove it, to build without it.
CONTENT_DIR="${JUNE_CONTENT_DIR:-}"
if [ -z "$CONTENT_DIR" ]; then
  for cand in "$ARCH/capture/upstream-content" "$LR/upstream-content"; do
    if [ -d "$cand" ]; then
      CONTENT_DIR="$cand"
      break
    fi
  done
fi
if [ -d "$CONTENT_DIR" ]; then
  cp -a "$CONTENT_DIR" "$DEST/upstream-content"
  echo "[*] preset content included: $(du -sh "$DEST/upstream-content" | cut -f1)"
else
  echo "[!] preset content not found at $CONTENT_DIR; guided programs (proof, slow cook, recipes) will be unavailable" >&2
fi
cp "$ARCH/tools/orchestrator/boot_junelocal_init.img" "$DEST/"
cp "$ARCH/packaging/generate_certs.sh" "$DEST/"
cp "$ARCH/packaging/sign_leaf.sh" "$DEST/"
cp "$LR/june-rcm/jetson-tk1.bct" "$DEST/"
cp "$LR/june-rcm/u-boot/u-boot-dtb-tegra.bin" "$DEST/"

if [ -f /tmp/tegrarcm ]; then
  cp /tmp/tegrarcm "$DEST/tegrarcm"
else
  cp "$LR/june-rcm/tegrarcm/src/tegrarcm" "$DEST/tegrarcm"
fi

# --- June Local identity seed assets ---------------------------------------
# The June apps need one Android AccountManager row (account type
# "com.junelife.user", authtoken "Full access") in /data.  A factory reset wipes
# that DB, which leaves the oven "disconnected" even with a correct install, so
# the installer ships a per-oven seed and june_reaper.sh installs it before
# Android starts when the row is missing.  The seed is generated at install time
# from accounts_template.db; the template is cloned from a captured oven DB with
# every account/authtoken/extra row stripped, so the captured cloud token never
# ships in the ISO.  See docs/identity-generation.md.
cat > "$DEST/generate_identity.py" <<'PYEOF'
#!/usr/bin/env python3
"""June Local per-oven identity seed generator.

The June oven apps read one Android AccountManager account row from
/data/system/users/0/accounts.db: account type "com.junelife.user", authtoken
type "Full access".  A factory reset wipes that DB, which drops the JCM's
bearer token and leaves the oven "disconnected" even when the install itself is
correct.  The installer therefore ships a seed DB in /system, and
june_reaper.sh installs it before Android starts when the June row is missing.

Commands:
  make-template SRC_CAPTURED_DB OUT_DB
      Build-time.  Clone the schema and the `meta` rows of a captured oven
      accounts.db into a template DB with NO account/authtoken/extra rows, so
      the captured cloud token never ships in the ISO.  The schema is copied
      from sqlite_master verbatim (with PRAGMA user_version); nothing is
      hand-authored.

  make-seed TEMPLATE_DB OUT_DB TOKEN
      Install-time.  Clone the template, replace the (empty) rows with one June
      account, the per-oven "Full access" token and the "Refresh access" extra,
      then verify integrity and that no other v2: token is present.

  mint-token
      Print a fresh token in the observed cloud format: v2: + 28 chars of
      URL-safe base64 (21 random bytes).

  check DB
      Print a short integrity/summary of a template or seed DB.

See docs/identity-generation.md for the design and the account schema.
"""

import base64
import hashlib
import os
import re
import shutil
import sqlite3
import sys
import urllib.parse

ACCOUNT_TYPE = "com.junelife.user"
ACCOUNT_NAME = "June Oven"
AUTHTOKEN_FULL = "Full access"
AUTHTOKEN_REFRESH = "Refresh access"
REFRESH_VALUE = "Refresh access to account"
REQUIRED_TABLES = ("accounts", "authtokens", "extras")
META_TABLE = "meta"
TOKEN_RE = re.compile(r"v2:[A-Za-z0-9_-]{28}")
TOKEN_BYTES_RE = re.compile(rb"v2:[A-Za-z0-9_-]{20,}")


def fail(message):
    sys.stderr.write("generate_identity.py: error: %s\n" % message)
    raise SystemExit(1)


def connect_ro(path):
    if not os.path.isfile(path):
        fail("missing database: %s" % path)
    # sqlite3 opens read/write by default; mode=ro keeps the captured template
    # untouched (it may live on a read-only or shared build volume).
    uri = "file:%s?mode=ro" % urllib.parse.quote(path)
    try:
        return sqlite3.connect(uri, uri=True)
    except sqlite3.Error as exc:
        fail("cannot open %s: %s" % (path, exc))


def table_names(conn):
    return {row[0] for row in conn.execute("SELECT name FROM sqlite_master WHERE type='table'")}


def scan_for_tokens(blob, label):
    found = TOKEN_BYTES_RE.findall(blob)
    if found:
        fail("%s contains v2: token data (%d match(es)); refusing to ship the "
             "captured cloud token" % (label, len(found)))


def schema_statements(conn):
    rows = conn.execute(
        "SELECT sql FROM sqlite_master WHERE sql IS NOT NULL "
        "AND name NOT LIKE 'sqlite_%' "
        "ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'index' THEN 1 ELSE 2 END, name"
    ).fetchall()
    if not rows:
        fail("captured database has no schema")
    return [row[0] for row in rows]


def integrity_ok(path):
    db = sqlite3.connect(path)
    try:
        result = db.execute("PRAGMA integrity_check").fetchone()[0]
    finally:
        db.close()
    if result != "ok":
        fail("integrity_check failed for %s: %s" % (path, result))


def summarize(path):
    db = sqlite3.connect(path)
    try:
        june_accounts = db.execute(
            "SELECT count(*) FROM accounts WHERE type=?", (ACCOUNT_TYPE,)
        ).fetchone()[0]
        accounts = db.execute("SELECT count(*) FROM accounts").fetchone()[0]
        authtokens = db.execute("SELECT count(*) FROM authtokens").fetchone()[0]
        extras = db.execute("SELECT count(*) FROM extras").fetchone()[0]
        version = db.execute("PRAGMA user_version").fetchone()[0]
    finally:
        db.close()
    integrity_ok(path)
    print("check: %s integrity ok, accounts %d total / %d June, authtokens %d, "
          "extras %d, user_version %d"
          % (path, accounts, june_accounts, authtokens, extras, version))
    return june_accounts


def make_template(src_path, out_path):
    src = connect_ro(src_path)
    try:
        names = table_names(src)
        missing = [name for name in REQUIRED_TABLES if name not in names]
        if missing:
            fail("captured DB is missing table(s) %s; it is not an Android "
                 "accounts database" % ", ".join(missing))
        if META_TABLE not in names:
            fail("captured DB has no %s table; refusing to hand-author the "
                 "Android schema version" % META_TABLE)
        statements = schema_statements(src)
        meta_cols = [row[1] for row in src.execute("PRAGMA table_info(%s)" % META_TABLE)]
        if not meta_cols:
            fail("captured DB %s table has no columns" % META_TABLE)
        meta_rows = src.execute("SELECT * FROM %s" % META_TABLE).fetchall()
        user_version = src.execute("PRAGMA user_version").fetchone()[0]
    finally:
        src.close()

    for statement in statements:
        scan_for_tokens(statement.encode("utf-8", "replace"), "captured schema")

    if os.path.exists(out_path):
        os.remove(out_path)
    out = sqlite3.connect(out_path)
    try:
        out.execute("PRAGMA secure_delete=ON")
        for statement in statements:
            out.execute(statement)
        if meta_rows:
            columns = ", ".join('"%s"' % col.replace('"', '""') for col in meta_cols)
            marks = ", ".join(["?"] * len(meta_cols))
            out.executemany(
                "INSERT INTO %s (%s) VALUES (%s)" % (META_TABLE, columns, marks),
                meta_rows,
            )
        out.commit()
    finally:
        out.close()

    # The template must carry the schema/meta only: no rows and no captured
    # token bytes, including stale bytes left in free pages.
    try:
        blob = open(out_path, "rb").read()
    except OSError as exc:
        fail("cannot read back %s: %s" % (out_path, exc))
    scan_for_tokens(blob, "generated template")
    db = sqlite3.connect(out_path)
    try:
        for table in REQUIRED_TABLES:
            count = db.execute("SELECT count(*) FROM %s" % table).fetchone()[0]
            if count != 0:
                fail("template table %s has %d row(s); the template must be "
                     "row-free" % (table, count))
    finally:
        db.close()
    if user_version:
        db = sqlite3.connect(out_path)
        try:
            db.execute("PRAGMA user_version=%d" % int(user_version))
            db.commit()
        finally:
            db.close()
    summarize(out_path)
    print("template: %s (%d schema statement(s), %d meta row(s))"
          % (out_path, len(statements), len(meta_rows)))


def make_seed(template_path, out_path, token):
    if not TOKEN_RE.fullmatch(token):
        fail("token does not match v2:<28 url-safe base64 chars>: %r" % token)
    if not os.path.isfile(template_path):
        fail("missing accounts template: %s" % template_path)
    shutil.copyfile(template_path, out_path)
    db = sqlite3.connect(out_path)
    try:
        db.execute("PRAGMA secure_delete=ON")
        db.execute("DELETE FROM accounts")
        db.execute("DELETE FROM authtokens")
        db.execute("DELETE FROM extras")
        cursor = db.execute(
            "INSERT INTO accounts (name, type) VALUES (?, ?)",
            (ACCOUNT_NAME, ACCOUNT_TYPE),
        )
        account_id = cursor.lastrowid
        db.executemany(
            "INSERT INTO authtokens (accounts_id, type, authtoken) VALUES (?, ?, ?)",
            [(account_id, AUTHTOKEN_FULL, token),
             (account_id, AUTHTOKEN_REFRESH, token)],
        )
        db.execute(
            "INSERT INTO extras (accounts_id, key, value) VALUES (?, ?, ?)",
            (account_id, AUTHTOKEN_REFRESH, REFRESH_VALUE),
        )
        db.commit()
    finally:
        db.close()

    # Prove the seed contains this oven's token and no other v2: credential.
    try:
        blob = open(out_path, "rb").read()
    except OSError as exc:
        fail("cannot read back %s: %s" % (out_path, exc))
    found = {match.group(0).decode("ascii") for match in TOKEN_BYTES_RE.finditer(blob)}
    foreign = sorted(found - {token})
    if foreign:
        fail("seed contains %d token(s) other than the minted one; refusing "
             "(the captured cloud token must not be reused)" % len(foreign))
    count = summarize(out_path)
    if count != 1:
        fail("seed has %d June account row(s), expected exactly 1" % count)
    db = sqlite3.connect(out_path)
    try:
        stored = db.execute(
            "SELECT authtoken FROM authtokens WHERE accounts_id=? AND type=?",
            (1, AUTHTOKEN_FULL),
        ).fetchone()
    finally:
        db.close()
    if not stored or stored[0] != token:
        fail("seed does not carry the minted Full access token")
    digest = hashlib.sha256(token.encode("ascii")).hexdigest()[:8]
    print("seed: %s (one %s account, token sha256 prefix %s)"
          % (out_path, ACCOUNT_TYPE, digest))


def mint_token():
    raw = base64.urlsafe_b64encode(os.urandom(21)).decode("ascii").rstrip("=")
    token = "v2:" + raw
    if not TOKEN_RE.fullmatch(token):
        fail("minted token has the wrong shape: %r" % token)
    print(token)


def usage():
    sys.stderr.write(
        "usage: generate_identity.py make-template SRC_CAPTURED_DB OUT_DB\n"
        "       generate_identity.py make-seed TEMPLATE_DB OUT_DB TOKEN\n"
        "       generate_identity.py mint-token\n"
        "       generate_identity.py check DB\n"
    )


def main(argv):
    if len(argv) < 2:
        usage()
        return 2
    command = argv[1]
    try:
        if command == "make-template" and len(argv) == 4:
            make_template(argv[2], argv[3])
        elif command == "make-seed" and len(argv) == 5:
            make_seed(argv[2], argv[3], argv[4])
        elif command == "mint-token" and len(argv) == 2:
            mint_token()
        elif command == "check" and len(argv) == 3:
            summarize(argv[2])
        else:
            usage()
            return 2
    except sqlite3.Error as exc:
        fail("sqlite error: %s" % exc)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PYEOF
chmod 0755 "$DEST/generate_identity.py"

# Build the row-free template from a captured oven DB.  This step is required:
# without it the ISO would install without account self-heal, so a factory-reset
# oven would stay disconnected.  python3 is already required for the installer
# GUI; on a Git-Bash/Windows build host `python` may be a Windows binary, so
# convert the one absolute path we hand it via cygpath.
PYTHON="${PYTHON:-}"
if [ -z "$PYTHON" ]; then
  for candidate in python3 python; do
    if command -v "$candidate" >/dev/null 2>&1; then
      PYTHON="$candidate"
      break
    fi
  done
fi
identity_template_src="${JUNE_ACCOUNTS_TEMPLATE:-}"
if [ -z "$identity_template_src" ]; then
  for candidate in \
    "$ROOT/local-resources/june-arch/capture/factory-reset-20260923/accounts_inject.db" \
    "$ARCH/capture/factory-reset-20260923/accounts_inject.db" \
    "$LR/capture/factory-reset-20260923/accounts_inject.db" \
    "$ARCH/capture/accounts_inject.db"; do
    if [ -f "$candidate" ]; then
      identity_template_src="$candidate"
      break
    fi
  done
fi
identity_template_missing() {
  # Dev-only escape hatch, clearly named: a published/source-only tree without
  # the captured DB can still build, but the ISO cannot self-heal after a reset.
  if [ "${JUNE_ALLOW_NO_IDENTITY_TEMPLATE:-0}" = "1" ]; then
    echo "[!] JUNE_ALLOW_NO_IDENTITY_TEMPLATE=1: building without an identity seed template." >&2
    echo "[!] The ISO will install without account self-heal; a factory-reset oven will stay disconnected." >&2
    return 0
  fi
  echo "[-] No captured June accounts DB found; refusing to build the installer." >&2
  echo "[-] Looked for: ${identity_template_src:-local-resources/june-arch/capture/factory-reset-20260923/accounts_inject.db}" >&2
  echo "[-] Set JUNE_ACCOUNTS_TEMPLATE=/path/to/accounts_inject.db (captured pre-reset UDA) or place" >&2
  echo "[-] it at local-resources/june-arch/capture/factory-reset-20260923/accounts_inject.db, then re-run." >&2
  echo "[-] Without it the ISO installs without account self-heal; see docs/identity-generation.md." >&2
  exit 1
}
if [ ! -f "$identity_template_src" ]; then
  identity_template_missing
else
  if [ -z "$PYTHON" ]; then
    echo "[-] python3 is required to build the identity seed template ($DEST/accounts_template.db)." >&2
    echo "[-] Install python3 on the build host, then re-run prepare_assets.sh." >&2
    echo "[-] Without it the ISO installs without account self-heal; see docs/identity-generation.md." >&2
    exit 1
  fi
  identity_template_arg="$identity_template_src"
  if command -v cygpath >/dev/null 2>&1; then
    identity_template_arg="$(cygpath -w "$identity_template_src")"
  fi
  if ! "$PYTHON" "$DEST/generate_identity.py" make-template \
      "$identity_template_arg" "$DEST/accounts_template.db"; then
    echo "[-] Could not build the identity seed template from $identity_template_src." >&2
    echo "[-] Refusing to build an installer that cannot self-heal after a factory reset." >&2
    echo "[-] See docs/identity-generation.md." >&2
    exit 1
  fi
  echo "[*] identity seed template generated from $(basename "$identity_template_src")"
fi

cat > "$DEST/install.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE="${1:-prepackaged}"
JCM_PERSIST=0
# Optional Chef cook-modes payload (widened primitive whitelist + plan_index).
CHEF_SRC=""
if [ -f "$DIR/Chef_cookmodes_signed.apk" ]; then
  CHEF_SRC="$DIR/Chef_cookmodes_signed.apk"
  export CHEF_SRC
fi
EXPECTED_SIZE="7818182656"
TEGRARCM="$DIR/tegrarcm"
GUIDE_URL="https://project-july.org/setup"
BUILD_ID="$(cat "$DIR/BUILD_ID" 2>/dev/null || echo unknown)"
VERSION="$(cat "$DIR/VERSION" 2>/dev/null || echo 0.8)"
export JUNE_VERSION="$VERSION"
export JUNE_BUILD_ID="$BUILD_ID"
echo "[*] June Local $VERSION (build $BUILD_ID)"

# Given a mounted source such as /dev/sda1, return the whole disk (/dev/sda).
disk_from_source() {
  local src="$1" base parent
  case "$src" in
    /dev/*) ;;
    *) return 1 ;;
  esac
  base="${src##*/}"

  if [ -e "/sys/class/block/$base/partition" ]; then
    parent="$(basename "$(dirname "$(readlink -f "/sys/class/block/$base")")")"
    [ -n "$parent" ] || return 1
    printf '/dev/%s\n' "$parent"
    return 0
  fi

  if [ -b "$src" ] && [ -d "/sys/class/block/$base" ]; then
    printf '%s\n' "$src"
    return 0
  fi

  return 1
}

# The disk we booted from (normally the live USB) is never the oven.
boot_or_root_disk() {
  local target src dev
  for target in /run/live/medium /lib/live/mount/medium /; do
    src="$(findmnt -n -o SOURCE --target "$target" 2>/dev/null || true)"
    case "$src" in
      ""|overlay|tmpfs) continue ;;
    esac
    dev="$(disk_from_source "$src" 2>/dev/null || true)"
    if [ -n "$dev" ]; then
      printf '%s\n' "$dev"
      return 0
    fi
  done
  return 1
}

boot_media_source() {
  local target src
  for target in /run/live/medium /lib/live/mount/medium; do
    src="$(findmnt -n -o SOURCE --target "$target" 2>/dev/null || true)"
    case "$src" in
      ""|overlay|tmpfs) continue ;;
    esac
    printf '%s\n' "$src"
    return 0
  done
  return 1
}

# Returns success when a file can really be created in a mountpoint.  This is
# stricter than checking the "ro" mount option: under some live-boot paths the
# mount table and the actual superblock flags disagree.
ventoy_write_probe() {
  local mnt="$1" probe
  [ -d "$mnt" ] || return 1
  probe="$mnt/.june-write-probe.$$"
  ( umask 077; : > "$probe" ) 2>/dev/null || return 1
  rm -f "$probe" 2>/dev/null || true
  return 0
}

# Try to make an already-mounted filesystem writable.
ventoy_remount_rw() {
  local mnt="$1" opts
  opts="$(findmnt -n -o OPTIONS --target "$mnt" 2>/dev/null | tr ',' '\n' || true)"
  if printf '%s\n' "$opts" | grep -qx 'ro'; then
    mount -o remount,rw "$mnt" 2>/dev/null || true
  fi
}

# Print every mountpoint for a partition, including mounts whose source is a
# loop device backed by it (the live system can end up with the boot USB
# mounted through /dev/loopN, which `findmnt --source /dev/sdX` never matches).
mounts_for_partition() {
  local part lp
  findmnt -rno TARGET --source "$part" 2>/dev/null || true
  command -v losetup >/dev/null 2>&1 || return 0
  while IFS= read -r lp; do
    [ -n "$lp" ] || continue
    findmnt -rno TARGET --source "$lp" 2>/dev/null || true
  done < <(losetup -j "$part" 2>/dev/null | cut -d: -f1)
}

find_june_disk() {
  local excluded b dev sz
  excluded="$(boot_or_root_disk || true)"
  for b in /sys/block/*; do
    [ -e "$b/device" ] || continue
    dev="/dev/$(basename "$b")"
    [ -b "$dev" ] || continue
    if [ -n "$excluded" ] && [ "$dev" = "$excluded" ]; then
      continue
    fi
    sz="$(blockdev --getsize64 "$dev" 2>/dev/null || echo 0)"
    if [ "$sz" = "$EXPECTED_SIZE" ]; then
      printf '%s\n' "$dev"
      return 0
    fi
  done
  return 1
}

wait_for_ums() {
  echo "[*] Looking for an existing June UMS disk (up to 15s)..."
  for _ in $(seq 1 15); do
    DEV="$(find_june_disk || true)"
    if [ -n "$DEV" ] && [ -b "$DEV" ]; then
      echo "[*] target disk: $DEV (excluded boot/system disk: ${BOOT_OR_ROOT_DISK:-none})"
      return 0
    fi
    sleep 1
  done

  DEV="$(find_june_disk || true)"
  if [ -n "$DEV" ] && [ -b "$DEV" ]; then
    echo "[*] target disk: $DEV (excluded boot/system disk: ${BOOT_OR_ROOT_DISK:-none})"
    export DEV
    return 0
  fi

  local waited=0
  echo "[*] Waiting for June APX device (0955:7740). Put the oven in RCM now (SW2 + power)."
  for _ in $(seq 1 120); do
    if command -v lsusb >/dev/null 2>&1 && lsusb 2>/dev/null | grep -qi '0955:7740'; then
      break
    fi
    waited=$(( waited + 1 ))
    if [ $(( waited % 5 )) -eq 0 ]; then
      echo "[*] Still waiting for APX 0955:7740: $(( 120 - waited ))s remaining..."
    fi
    sleep 1
  done

  echo "[*] Oven is not in UMS mode. Loading U-Boot with tegrarcm..."
  command -v pkill >/dev/null 2>&1 && pkill -9 -x tegrarcm 2>/dev/null || true
  if command -v lsusb >/dev/null 2>&1; then
    echo "[*] APX devices before tegrarcm:"
    lsusb 2>/dev/null | grep -i '0955:7740' || echo "    none"
  fi
  tg_rc=1
  tg_attempt=1
  tg_stale=0
  tg_out="$(mktemp /tmp/tegrarcm.XXXXXX.log)"
  while [ "$tg_attempt" -le 3 ]; do
    tg_rc=0
    unset LIBUSB_DEBUG
    # Only the last attempt is verbose: the libusb debug log is huge.
    if [ "$tg_attempt" -eq 3 ]; then
      export LIBUSB_DEBUG=4
    fi
    echo "[*] tegrarcm attempt $tg_attempt/3 (45s USB timeout)..."
    if command -v timeout >/dev/null 2>&1; then
      timeout 90 "$TEGRARCM" --soc=124 --usb-timeout=45000 \
        --bct="$DIR/jetson-tk1.bct" \
        --bootloader="$DIR/u-boot-dtb-tegra.bin" \
        --loadaddr=0x80108000 >"$tg_out" 2>&1 || tg_rc=$?
    else
      "$TEGRARCM" --soc=124 --usb-timeout=45000 \
        --bct="$DIR/jetson-tk1.bct" \
        --bootloader="$DIR/u-boot-dtb-tegra.bin" \
        --loadaddr=0x80108000 >"$tg_out" 2>&1 || tg_rc=$?
    fi
    cat "$tg_out"
    if [ "$tg_rc" -eq 0 ]; then
      break
    fi
    echo "[!] tegrarcm attempt $tg_attempt failed (exit $tg_rc)." >&2
    # The boot ROM only answers one RCM session per power-on (it sends the
    # chip UID once).  Once it stops answering, retrying without a power
    # cycle just burns another USB timeout, so stop and ask for re-entry.
    if grep -q 'Resource temporarily unavailable' "$tg_out"; then
      tg_stale=1
      break
    fi
    tg_attempt=$((tg_attempt + 1))
    if [ "$tg_attempt" -le 3 ]; then
      sleep 3
    fi
  done
  unset LIBUSB_DEBUG
  rm -f "$tg_out"
  if [ "$tg_rc" -ne 0 ]; then
    if [ "$tg_stale" -eq 1 ]; then
      echo "[-] The oven stopped answering in RCM mode (tegrarcm: Resource temporarily unavailable)." >&2
      echo "[-] Unplug the oven power, wait 5 seconds, then hold SW2 and power it on again (keep holding ~10s), and retry." >&2
    else
      echo "[-] tegrarcm failed after 3 attempts (last exit $tg_rc)." >&2
      echo "[-] USB bus errors usually mean the cable or port: try another cable and a direct USB port (no hub), unplug/replug the oven, then retry." >&2
      echo "[-] Also make sure the oven is in RCM mode (hold SW2, power on, keep holding ~10s)." >&2
    fi
    return 1
  fi
  sleep 2

  echo "[*] Waiting for the June eMMC block device (up to 60s)..."
  for _ in $(seq 1 60); do
    DEV="$(find_june_disk || true)"
    if [ -n "$DEV" ] && [ -b "$DEV" ]; then
      echo "[*] target disk: $DEV (excluded boot/system disk: ${BOOT_OR_ROOT_DISK:-none})"
      export DEV
      return 0
    fi
    if [ $(( _ % 5 )) -eq 0 ]; then
      echo "[*] Still waiting for the June eMMC: $(( 60 - _ ))s remaining..."
    fi
    sleep 1
  done

  echo "[-] June eMMC block device never appeared." >&2
  echo "[-] Excluded boot/system disk: ${BOOT_OR_ROOT_DISK:-none}" >&2
  echo "[-] Make sure the oven is in RCM (SW2 + power) and USB is connected." >&2
  return 1
}

BOOT_OR_ROOT_DISK="$(boot_or_root_disk || true)"

if [ "$MODE" = "generate" ]; then
  # Explicit provenance for deploy_onoven.sh: custom = per-home CA plus no-pin
  # network clients; the per-home CA is added to the oven's trust store.
  export INSTALL_MODE="custom"
  export CA_MODE="per-home"
  OVEN_IP="${2:-}"
  OVEN_HOST="${3:-}"
  PERSIST="${4:-}"
  [ -n "$OVEN_IP" ] || read -rp "Oven LAN IP: " OVEN_IP
  [ -n "$OVEN_HOST" ] || read -rp "Hostname (optional): " OVEN_HOST
  OVEN_HOST="${OVEN_HOST:-june.local}"
  export OVEN_HOST
  export JUNE_OVEN_IP="$OVEN_IP"
  if [ "$PERSIST" = "persist" ]; then
    JCM_PERSIST=1
  fi
  mkdir -p "$DIR/certs"
  JUNE_IP_SAN="$OVEN_IP" JUNE_HOSTNAME="$OVEN_HOST" bash "$DIR/generate_certs.sh" "$DIR/certs"
  export CERT_SRC="$DIR/certs/fullchain.crt"
  export KEY_SRC="$DIR/certs/leaf.key"
  export JCM_SRC="$DIR/JuneCloudMessaging_nopin_signed.apk"
  # The oven-side pairing UI (Remotes) uses the same pinned network client, so
  # a per-home CA needs it patched too or pairing fails oven-side.
  export REMOTES_SRC="$DIR/Remotes_nopin_signed.apk"
  # MediaCapturer also pins June's cert. Patching it lets the cook-time
  # stills reach june-local (and thus the companion/HA) instead of being
  # recorded and discarded.
  export MEDIACAPTURER_SRC="$DIR/MediaCapturer_nopin_signed.apk"
  # Analytics has the same pin; replacing it stops the once-per-second TLS
  # retry noise against june-local. Analytics is not used for cooking.
  export ANALYTICS_SRC="$DIR/Analytics_nopin_signed.apk"
  CA_HASH="$(openssl x509 -in "$DIR/certs/ca.crt" -subject_hash_old -noout)"
  export CA_SRC="$DIR/certs/ca.crt"
  export CA_HASH="$CA_HASH"
  if [ "$(basename "$JCM_SRC")" != "JuneCloudMessaging_nopin_signed.apk" ]; then
    echo "[-] custom CA install must use the no-pin JCM, but JCM_SRC=$JCM_SRC" >&2
    exit 1
  fi
  VENTOY_MNT="$(bash "$DIR/backup.sh" ventoy-dir 2>>/tmp/june-dest.log || true)"
  if [ -n "$VENTOY_MNT" ] && mkdir -p "$VENTOY_MNT/june-certs" 2>/dev/null; then
    # Saving the CA to the USB stick is a convenience: never let a write
    # problem here abort the install (the CA stays in $DIR/certs either way).
    if cp "$DIR/certs/ca.crt" "$VENTOY_MNT/june-certs/ca.crt" \
      && cp "$DIR/certs/leaf.crt" "$VENTOY_MNT/june-certs/leaf.crt" \
      && cp "$DIR/certs/fullchain.crt" "$VENTOY_MNT/june-certs/fullchain.crt"; then
      sync 2>/dev/null || true
      echo "[*] CA cert saved to $VENTOY_MNT/june-certs/ca.crt"
    else
      echo "[!] CA save: could not write every file to $VENTOY_MNT/june-certs (continuing)." >&2
      echo "[!] CA save: the generated CA is still available at $DIR/certs." >&2
    fi
  else
    echo "[-] CA save: no writable Ventoy data partition found (details in /tmp/june-dest.log)." >&2
    echo "[-] CA save: the generated CA is still available at $DIR/certs." >&2
  fi
else
  # Explicit provenance for deploy_onoven.sh: basic = factory apps plus the
  # bundled project chain.  That chain's leaf is issued by the project CA, which
  # is installed as a trust anchor in the oven's trust store (CA_MODE=project):
  # without it a clean oven rejects june-local's TLS certificate and the stock
  # June apps never connect.
  export INSTALL_MODE="basic"
  export CA_MODE="project"
  # Standard ("prepackaged") install keeps the oven's factory
  # JuneCloudMessaging app, so there is no patch payload to persist unless the
  # user opts in. Persistence is only meaningful together with the no-pin
  # patch: when requested, stage the no-pin APK in /system so it survives a
  # factory reset. Never stage the factory APK under the patch name - it would
  # shadow a real patch on the next custom install and silently keep the oven
  # pinned to the June cloud.
  OVEN_HOST="${2:-}"
  PERSIST="${3:-}"
  export OVEN_HOST
  # Be forgiving of older GUI callers that passed only "persist" as the second
  # argument; the default oven name is used in that case.
  if [ "$OVEN_HOST" = "persist" ]; then
    PERSIST="persist"
    OVEN_HOST=""
    export OVEN_HOST
  fi
  if [ "$PERSIST" = "persist" ]; then
    JCM_PERSIST=1
    export JCM_SRC="$DIR/JuneCloudMessaging_nopin_signed.apk"
  else
    export JCM_SRC="$DIR/JuneCloudMessaging.apk"
  fi
  # Prepackaged installs serve the bundled project chain; the project CA that
  # signs it is installed as a trust anchor by deploy_onoven.sh.
  export REMOTES_SRC=""
  export CERT_SRC="$DIR/junelife_chain_godaddy_intermediate.crt"
  export KEY_SRC="$DIR/june-local.key"
  # The factory MediaCapturer pins June's original certificates, so its
  # cook-time stills cannot reach june-local. Use the no-pin build in basic
  # mode too; without it the live view is blank while a cook is running.
  export MEDIACAPTURER_SRC="$DIR/MediaCapturer_nopin_signed.apk"
  # The project CA alone (not the chain) is served at /local/ca.crt for the
  # phone to install, and deploy_onoven.sh installs the same file as
  # /system/etc/security/cacerts/$CA_HASH.0 in the oven.  Without the oven-side
  # anchor a clean oven rejects june-local's TLS certificate.
  CA_SRC="$DIR/junelife_ca.crt"
  CA_HASH="$(openssl x509 -in "$CA_SRC" -subject_hash_old -noout)"
  export CA_SRC CA_HASH
fi

echo "[debug] MODE=$MODE"
echo "[debug] INSTALL_MODE=$INSTALL_MODE CA_MODE=$CA_MODE"
echo "[debug] JCM_PERSIST=$JCM_PERSIST"
echo "[debug] CERT_SRC=$(basename "$CERT_SRC")"
echo "[debug] JCM_SRC=$(basename "$JCM_SRC")"
installer_hashes=("$CERT_SRC" "$JCM_SRC")
if [ -n "${MEDIACAPTURER_SRC:-}" ]; then
  echo "[debug] MEDIACAPTURER_SRC=$(basename "$MEDIACAPTURER_SRC")"
  installer_hashes+=("$MEDIACAPTURER_SRC")
fi
if [ -n "${ANALYTICS_SRC:-}" ]; then
  echo "[debug] ANALYTICS_SRC=$(basename "$ANALYTICS_SRC")"
  installer_hashes+=("$ANALYTICS_SRC")
fi
if [ -n "${CHEF_SRC:-}" ]; then
  echo "[debug] CHEF_SRC=$(basename "$CHEF_SRC")"
  installer_hashes+=("$CHEF_SRC")
fi
sha256sum "${installer_hashes[@]}"

wait_for_ums

export BIN_SRC="$DIR/june-local-linux-armv7"
export CAMERAD_SRC="$DIR/june-camerad-linux-armv7"
export BOOT_SRC="$DIR/boot_junelocal_init.img"
export BOOTCLEAR_SRC="$DIR/june_bootclear.sh.onoven"
export COMPANION_STATE_SRC="$DIR/companions_state.json"
export FLASH_BOOT=1
export JCM_PERSIST

INSTALL_LOG="${JUNE_INSTALL_LOG_DIR:-/tmp}/june-install-$(date +%Y%m%d-%H%M%S).log"
mkdir -p "$(dirname "$INSTALL_LOG")"
echo "[*] Install log: $INSTALL_LOG"

# Always try to leave the log on the USB stick, on success and failure alike.
# This runs as root and can write through a busy/loop-mounted boot USB, where
# the menu-level copy can miss.
save_log_to_usb() {
  local mnt="${VENTOY_MNT:-}"
  if [ -z "$mnt" ]; then
    mnt="$(bash "$DIR/backup.sh" ventoy-dir 2>>/tmp/june-dest.log || true)"
  fi
  [ -n "$mnt" ] && [ -d "$mnt" ] || return 0
  mkdir -p "$mnt/june-logs" 2>/dev/null || return 0
  cp -f "$INSTALL_LOG" "$mnt/june-logs/" 2>/dev/null || return 0
  if [ -d "$DIR/certs" ]; then
    mkdir -p "$mnt/june-certs" 2>/dev/null || true
    cp -f "$DIR/certs/ca.crt" "$mnt/june-certs/" 2>/dev/null || true
    cp -f "$DIR/certs/leaf.crt" "$mnt/june-certs/" 2>/dev/null || true
    cp -f "$DIR/certs/fullchain.crt" "$mnt/june-certs/" 2>/dev/null || true
  fi
  sync 2>/dev/null || true
  echo "[*] Installer log saved to $mnt/june-logs/$(basename "$INSTALL_LOG")"
}
trap save_log_to_usb EXIT

if ! bash "$DIR/deploy_onoven.sh" 2>&1 | tee "$INSTALL_LOG"; then
  echo
  echo "[-] Deploy failed. Full log: $INSTALL_LOG" >&2
  echo "[-] The last successful step is the last line above." >&2
  exit 1
fi

echo
echo "=============================================================="
echo "  SUCCESS: June Local is installed on the oven"
echo "=============================================================="
echo
echo "Do this next, in order:"
echo "  1. Unplug the oven power."
echo "  2. Unplug the USB cable from this computer."
echo "  3. Close/reassemble the oven."
echo "  4. Power the oven on normally."
echo
if [ "$MODE" = "generate" ]; then
  echo "Phone setup:"
  echo "  1. Install companion_endpoint_signed.apk."
  echo "  2. Set the endpoint to $OVEN_IP (Connect by IP). The JuneDns helper advertises"
  echo "     $OVEN_HOST; use that name instead if it resolves. Add a local DNS entry if"
  echo "     multicast (mDNS) is blocked; the IP always works."
  echo "  3. Install the per-home CA on the phone: $DIR/certs/ca.crt"
  echo "  4. Open the June app and pair/control the oven."
else
  echo "Phone setup:"
  echo "  1. Install companion_endpoint_signed.apk."
  echo "  2. Use june.local, june2.local or june3.local: the JuneDns helper on the oven"
  echo "     advertises <name>.local. If multicast (mDNS) is blocked, add a local DNS"
  echo "     entry or use Connect by IP instead. The basic certificate does not include"
  echo "     the raw IP."
  echo "  3. Install the project CA on the phone so it trusts june-local:"
  echo "     download it from http://<oven-ip>/local/ca.crt"
  echo "  4. Set the companion app endpoint to june.local (or the name you used)."
  echo "  5. (Advanced) stock June app: point api.junelife.com and messaging.junelife.com"
  echo "     at the oven in your router DNS."
  echo "  6. Open the June app and pair/control the oven."
fi
echo
echo "How to know it worked: after phone setup, open the June app and try to"
echo "control the oven. Live temperature and cook controls working without"
echo "cloud/offline errors means the oven is running locally."
echo "You can also open http://<oven-ip>/local from any device on the same network."
echo
echo "If you need help: $GUIDE_URL"
EOF

cat > "$DEST/backup.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE="${1:-backup}"
EXPECTED_SIZE="7818182656"
TEGRARCM="$DIR/tegrarcm"
GUIDE_URL="https://project-july.org/setup"

disk_from_source() {
  local src="$1" base parent
  case "$src" in
    /dev/*) ;;
    *) return 1 ;;
  esac
  base="${src##*/}"
  if [ -e "/sys/class/block/$base/partition" ]; then
    parent="$(basename "$(dirname "$(readlink -f "/sys/class/block/$base")")")"
    [ -n "$parent" ] || return 1
    printf '/dev/%s\n' "$parent"
    return 0
  fi
  if [ -b "$src" ] && [ -d "/sys/class/block/$base" ]; then
    printf '%s\n' "$src"
    return 0
  fi
  return 1
}

boot_media_source() {
  local target src
  for target in /run/live/medium /lib/live/mount/medium; do
    src="$(findmnt -n -o SOURCE --target "$target" 2>/dev/null || true)"
    case "$src" in
      ""|overlay|tmpfs) continue ;;
    esac
    printf '%s\n' "$src"
    return 0
  done
  return 1
}

# Returns success when a file can really be created in a mountpoint.  This is
# stricter than checking the "ro" mount option: under some live-boot paths the
# mount table and the actual superblock flags disagree.
ventoy_write_probe() {
  local mnt="$1" probe
  [ -d "$mnt" ] || return 1
  probe="$mnt/.june-write-probe.$$"
  ( umask 077; : > "$probe" ) 2>/dev/null || return 1
  rm -f "$probe" 2>/dev/null || true
  return 0
}

# Try to make an already-mounted filesystem writable.
ventoy_remount_rw() {
  local mnt="$1" opts
  opts="$(findmnt -n -o OPTIONS --target "$mnt" 2>/dev/null | tr ',' '\n' || true)"
  if printf '%s\n' "$opts" | grep -qx 'ro'; then
    mount -o remount,rw "$mnt" 2>/dev/null || true
  fi
}

boot_or_root_disk() {
  local target src dev
  for target in /run/live/medium /lib/live/mount/medium /; do
    src="$(findmnt -n -o SOURCE --target "$target" 2>/dev/null || true)"
    case "$src" in
      ""|overlay|tmpfs) continue ;;
    esac
    dev="$(disk_from_source "$src" 2>/dev/null || true)"
    if [ -n "$dev" ]; then
      printf '%s\n' "$dev"
      return 0
    fi
  done
  return 1
}

boot_writable_destination() {
  local target src opts avail
  for target in /run/live/medium /lib/live/mount/medium; do
    src="$(findmnt -n -o SOURCE --target "$target" 2>/dev/null || true)"
    case "$src" in
      /dev/*) ;;
      *) continue ;;
    esac
    opts="$(findmnt -n -o OPTIONS --target "$target" 2>/dev/null | tr ',' '\n' || true)"
    if printf '%s\n' "$opts" | grep -qx 'ro'; then
      continue
    fi
    avail="$(df -B1 --output=avail "$target" 2>/dev/null | tail -n1 | tr -d ' ' || true)"
    if [ "${avail:-0}" -ge "$EXPECTED_SIZE" ]; then
      printf '%s\t%s\n' "$src" "$target"
      return 0
    fi
  done
  return 1
}

# Print every mountpoint for a partition, including mounts whose source is a
# loop device backed by it (the live system can end up with the boot USB
# mounted through /dev/loopN, which `findmnt --source /dev/sdX` never matches).
mounts_for_partition() {
  local part lp
  findmnt -rno TARGET --source "$part" 2>/dev/null || true
  command -v losetup >/dev/null 2>&1 || return 0
  while IFS= read -r lp; do
    [ -n "$lp" ] || continue
    findmnt -rno TARGET --source "$lp" 2>/dev/null || true
  done < <(losetup -j "$part" 2>/dev/null | cut -d: -f1)
}

# Print the writable Ventoy data partition as "<device><TAB><mountpoint>".
# The live system boots from this same stick under `toram`: live-boot unmounts
# the exFAT data partition and mounts a read-only tmpfs over the medium path,
# so there may be no existing mount to reuse.  In that case mount the real
# partition again, force it read-write, and prove writability with a probe
# file.  Every rejection is explained on stderr (the menu shows the details).
ventoy_data_partition() {
  local name part fstype label mnt avail candidate mounted_here loop_dev mount_err loop_err
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    part="/dev/$name"
    [ -b "$part" ] || continue
    fstype="$(lsblk -rno FSTYPE "$part" 2>/dev/null | head -n1)"
    label="$(lsblk -rno LABEL "$part" 2>/dev/null | head -n1)"
    case "${label,,}" in ventoy|june) ;; *) continue ;; esac
    case "${fstype,,}" in
      exfat|ntfs|ext4) ;;
      *)
        echo "[diag] $part ($label): filesystem '${fstype:-unknown}' cannot hold a >4 GB backup." >&2
        continue
        ;;
    esac

    # 1) Reuse an existing mountpoint if it can be made writable.
    while IFS= read -r candidate; do
      [ -n "$candidate" ] || continue
      ventoy_remount_rw "$candidate"
      if ! ventoy_write_probe "$candidate"; then
        echo "[diag] $part mounted at $candidate but not writable." >&2
        continue
      fi
      avail="$(df -B1 --output=avail "$candidate" 2>/dev/null | tail -n1 | tr -d ' ' || echo 0)"
      if [ "${avail:-0}" -lt "$EXPECTED_SIZE" ]; then
        echo "[diag] $part at $candidate has only $(( ${avail:-0} / 1000000000 )) GB free." >&2
        continue
      fi
      printf '%s\t%s\n' "$part" "$candidate"
      return 0
    done < <(mounts_for_partition "$part")

    # 2) Mount the real partition again (toram leaves it unmounted).
    mnt="/mnt/june-ventoy"
    mkdir -p "$mnt" 2>/dev/null || true
    if mountpoint -q "$mnt" 2>/dev/null; then
      umount "$mnt" 2>/dev/null || true
    fi
    mounted_here=0
    mount_err=""
    # Some boot paths flag the live USB partition read-only in the kernel
    # (Ventoy/dm). We run from RAM under `toram`, so clearing the flag is safe
    # and lets the writable probe below succeed.
    if [ "$(blockdev --getro "$part" 2>/dev/null || echo 0)" = "1" ]; then
      echo "[diag] $part: kernel read-only flag was set; clearing it (running from RAM)." >&2
      blockdev --setrw "$part" 2>/dev/null || true
    fi
    case "${fstype,,}" in
      exfat) command -v modprobe >/dev/null 2>&1 && modprobe exfat 2>/dev/null || true ;;
      ntfs) command -v modprobe >/dev/null 2>&1 && modprobe ntfs3 2>/dev/null || true ;;
    esac
    if mount_err="$(mount -o rw "$part" "$mnt" 2>&1)"; then
      mounted_here=1
    elif mount_err="$(mount "$part" "$mnt" 2>&1)"; then
      mounted_here=1
    else
      echo "[diag] $part ($fstype): direct mount failed: ${mount_err:-unknown error}" >&2
    fi
    loop_dev=""
    loop_created=0
    if [ "$mounted_here" != "1" ] && command -v losetup >/dev/null 2>&1; then
      # Prefer an existing loop device for this partition: creating a second
      # one can fail while the first is still attached.
      loop_dev="$(losetup -j "$part" 2>/dev/null | cut -d: -f1 | head -n1 || true)"
      if [ -z "$loop_dev" ]; then
        loop_dev="$(losetup -f --show "$part" 2>/dev/null || true)"
        [ -n "$loop_dev" ] && loop_created=1
      fi
      if [ -n "$loop_dev" ]; then
        if loop_err="$(mount -o rw "$loop_dev" "$mnt" 2>&1)"; then
          mounted_here=1
          echo "[diag] $part: mounted via $loop_dev because the partition was busy." >&2
        else
          echo "[diag] $part: loop mount via $loop_dev also failed: ${loop_err:-unknown error}" >&2
          [ "$loop_created" = "1" ] && losetup -d "$loop_dev" 2>/dev/null || true
          loop_dev=""
        fi
      fi
    fi
    if [ "$mounted_here" != "1" ]; then
      echo "[diag] $part: could not mount it again (last error: ${mount_err:-none}; live boot medium still busy?)." >&2
      continue
    fi
    ventoy_remount_rw "$mnt"
    if ! ventoy_write_probe "$mnt"; then
      echo "[diag] $part: remounted but still read-only." >&2
      umount "$mnt" 2>/dev/null || true
      [ "$loop_created" = "1" ] && losetup -d "$loop_dev" 2>/dev/null || true
      continue
    fi
    avail="$(df -B1 --output=avail "$mnt" 2>/dev/null | tail -n1 | tr -d ' ' || echo 0)"
    if [ "${avail:-0}" -lt "$EXPECTED_SIZE" ]; then
      echo "[diag] $part: only $(( ${avail:-0} / 1000000000 )) GB free after mounting." >&2
      umount "$mnt" 2>/dev/null || true
      [ "$loop_created" = "1" ] && losetup -d "$loop_dev" 2>/dev/null || true
      continue
    fi
    printf '%s\t%s\n' "$part" "$mnt"
    return 0
  done < <(lsblk -rno NAME,TYPE 2>/dev/null | awk '$2=="part"{print $1}')
  return 1
}

list_destinations() {
  local name dev type fstype label mnt tran parent removable origin avail mounted_here ventoy_line
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    dev="/dev/$name"
    [ -b "$dev" ] || continue
    # Query one field at a time: lsblk's multi-column output collapses empty
    # fields, which shifts columns and silently hides some partitions.
    type="$(lsblk -rno TYPE "$dev" 2>/dev/null | head -n1)"
    [ "$type" = "part" ] || continue
    fstype="$(lsblk -rno FSTYPE "$dev" 2>/dev/null | head -n1)"
    label="$(lsblk -rno LABEL "$dev" 2>/dev/null | head -n1)"
    mnt="$(lsblk -rno MOUNTPOINT "$dev" 2>/dev/null | head -n1)"
    tran="$(lsblk -rno TRAN "$dev" 2>/dev/null | head -n1)"
    case "${fstype,,}" in
      exfat|ntfs|ext4) ;;
      *)
        case "${label,,}" in
          ventoy|june) echo "[diag] $dev ($label): '${fstype:-unknown}' cannot hold a >4 GB image." >&2 ;;
        esac
        continue
        ;;
    esac
    parent="$(disk_from_source "$dev" 2>/dev/null || true)"
    [ -n "$parent" ] || continue
    [ -n "${JUNE_DISK:-}" ] && [ "$dev" = "$JUNE_DISK" ] && continue
    removable="$(cat "/sys/block/${parent#/dev/}/removable" 2>/dev/null || echo 0)"
    if [ "$tran" = "usb" ] || [ "$removable" = "1" ]; then
      origin="usb"
    else
      origin="internal"
    fi
    mounted_here=0
    case "${label,,}" in
      ventoy|june)
        # JUNE is the data partition of a stick made by June USB
        # (installer/usbtool); Ventoy sticks keep working.
        case "${label,,}" in
          june) label="June USB (current boot USB)" ;;
          *) label="Ventoy (current boot USB)" ;;
        esac
        ventoy_line="$(ventoy_data_partition || true)"
        if [ -n "$ventoy_line" ]; then
          mnt="${ventoy_line#*$'\t'}"
          mounted_here=0
        else
          echo "[diag] $dev: Ventoy probe failed; trying the generic mount path." >&2
          mnt="$(mounts_for_partition "$dev" | head -n1 || true)"
        fi
        ;;
      *)
        case "$mnt" in
          /run/live/*|/lib/live/*|/usr/lib/live/*|/|/boot|/usr|/var) continue ;;
        esac
        ;;
    esac
    if [ -z "$mnt" ]; then
      mnt="/mnt/june-probe"
      mkdir -p "$mnt" 2>/dev/null || true
      if mountpoint -q "$mnt" 2>/dev/null; then
        umount "$mnt" 2>/dev/null || true
      fi
      if ! mount "$dev" "$mnt" 2>/dev/null; then
        echo "[diag] $dev: could not mount it to check free space." >&2
        continue
      fi
      mounted_here=1
    fi
    if ! ventoy_write_probe "$mnt"; then
      echo "[diag] $dev at $mnt is read-only." >&2
      [ "$mounted_here" = "1" ] && umount "$mnt" 2>/dev/null || true
      continue
    fi
    avail="$(df -B1 --output=avail "$mnt" 2>/dev/null | tail -n1 | tr -d ' ' || echo 0)"
    if [ "${avail:-0}" -lt "$EXPECTED_SIZE" ]; then
      echo "[diag] $dev: only $(( ${avail:-0} / 1000000000 )) GB free, need 8 GB." >&2
      [ "$mounted_here" = "1" ] && umount "$mnt" 2>/dev/null || true
      continue
    fi
    printf '%s\t%s\t%s\t%s\n' "$dev" "${label:-${parent#/dev/}}" "$avail" "$origin"
    [ "$mounted_here" = "1" ] && umount "$mnt" 2>/dev/null || true
  done < <(lsblk -rno NAME,TYPE 2>/dev/null | awk '$2=="part"{print $1}')
}

resolve_explicit_destination() {
  local target="$1" dev="" mnt="" fstype avail mounted_here=0
  if [ -d "$target" ]; then
    mnt="$target"
    if ! mountpoint -q "$mnt" 2>/dev/null; then
      echo "[-] $mnt is a directory on the live system, not a mounted USB drive." >&2
      return 1
    fi
    dev="$(findmnt -rno SOURCE --target "$mnt" 2>/dev/null | head -n1 || true)"
  else
    dev="$target"
    if [ ! -b "$dev" ]; then
      echo "[-] $target is neither a block device nor a mountpoint." >&2
      return 1
    fi
    mnt="$(mounts_for_partition "$dev" | head -n1 || true)"
    if [ -n "$mnt" ]; then
      ventoy_remount_rw "$mnt"
      if ! ventoy_write_probe "$mnt"; then
        mnt=""
      fi
    fi
    if [ -z "$mnt" ]; then
      mnt="/mnt/june-backup"
      mkdir -p "$mnt" 2>/dev/null || true
      if mountpoint -q "$mnt" 2>/dev/null; then
        umount "$mnt" 2>/dev/null || true
      fi
      if ! mount -o rw "$dev" "$mnt" 2>/dev/null && ! mount "$dev" "$mnt" 2>/dev/null; then
        echo "[-] Could not mount $dev (is the live boot medium still using it?)." >&2
        return 1
      fi
      mounted_here=1
      ventoy_remount_rw "$mnt"
      if ! ventoy_write_probe "$mnt"; then
        echo "[-] $dev is read-only." >&2
        umount "$mnt" 2>/dev/null || true
        return 1
      fi
    fi
  fi
  fstype="$(stat -f -c %T "$mnt" 2>/dev/null || true)"
  case "$fstype" in exfat|ntfs|ext4) ;; *)
    echo "[-] ${dev:-$mnt} is $fstype, not a supported backup filesystem." >&2
    [ "$mounted_here" = "1" ] && umount "$mnt" 2>/dev/null || true
    return 1
    ;;
  esac
  if ! ventoy_write_probe "$mnt"; then
    echo "[-] ${dev:-$mnt} is not writable." >&2
    [ "$mounted_here" = "1" ] && umount "$mnt" 2>/dev/null || true
    return 1
  fi
  avail="$(df -B1 --output=avail "$mnt" 2>/dev/null | tail -n1 | tr -d ' ' || echo 0)"
  if [ "${avail:-0}" -lt "$EXPECTED_SIZE" ]; then
    echo "[-] ${dev:-$mnt} has only $(( ${avail:-0} / 1000000000 )) GB free; 8 GB needed." >&2
    [ "$mounted_here" = "1" ] && umount "$mnt" 2>/dev/null || true
    return 1
  fi
  printf '%s\t%s\n' "$dev" "$mnt"
}

find_june_disk() {
  local excluded b dev sz
  excluded="$(boot_or_root_disk || true)"
  for b in /sys/block/*; do
    [ -e "$b/device" ] || continue
    dev="/dev/$(basename "$b")"
    [ -b "$dev" ] || continue
    [ -n "$excluded" ] && [ "$dev" = "$excluded" ] && continue
    sz="$(blockdev --getsize64 "$dev" 2>/dev/null || echo 0)"
    if [ "$sz" = "$EXPECTED_SIZE" ]; then
      printf '%s\n' "$dev"
      return 0
    fi
  done
  return 1
}

wait_for_ums() {
  DEV="$(find_june_disk || true)"
  if [ -n "$DEV" ] && [ -b "$DEV" ]; then
    echo "[*] target disk: $DEV (excluded boot/system disk: ${BOOT_OR_ROOT_DISK:-none})"
    return 0
  fi
  local waited=0
  echo "[*] Waiting for June APX device (0955:7740). Put the oven in RCM now (SW2 + power)."
  for _ in $(seq 1 120); do
    if command -v lsusb >/dev/null 2>&1 && lsusb 2>/dev/null | grep -qi '0955:7740'; then
      break
    fi
    waited=$(( waited + 1 ))
    if [ $(( waited % 5 )) -eq 0 ]; then
      echo "[*] Still waiting for APX 0955:7740: $(( 120 - waited ))s remaining..."
    fi
    sleep 1
  done

  echo "[*] Oven is not in UMS mode. Loading U-Boot with tegrarcm..."
  command -v pkill >/dev/null 2>&1 && pkill -9 -x tegrarcm 2>/dev/null || true
  if command -v lsusb >/dev/null 2>&1; then
    echo "[*] APX devices before tegrarcm:"
    lsusb 2>/dev/null | grep -i '0955:7740' || echo "    none"
  fi
  tg_rc=1
  tg_attempt=1
  tg_stale=0
  tg_out="$(mktemp /tmp/tegrarcm.XXXXXX.log)"
  while [ "$tg_attempt" -le 3 ]; do
    tg_rc=0
    unset LIBUSB_DEBUG
    # Only the last attempt is verbose: the libusb debug log is huge.
    if [ "$tg_attempt" -eq 3 ]; then
      export LIBUSB_DEBUG=4
    fi
    echo "[*] tegrarcm attempt $tg_attempt/3 (45s USB timeout)..."
    if command -v timeout >/dev/null 2>&1; then
      timeout 90 "$TEGRARCM" --soc=124 --usb-timeout=45000 \
        --bct="$DIR/jetson-tk1.bct" \
        --bootloader="$DIR/u-boot-dtb-tegra.bin" \
        --loadaddr=0x80108000 >"$tg_out" 2>&1 || tg_rc=$?
    else
      "$TEGRARCM" --soc=124 --usb-timeout=45000 \
        --bct="$DIR/jetson-tk1.bct" \
        --bootloader="$DIR/u-boot-dtb-tegra.bin" \
        --loadaddr=0x80108000 >"$tg_out" 2>&1 || tg_rc=$?
    fi
    cat "$tg_out"
    if [ "$tg_rc" -eq 0 ]; then
      break
    fi
    echo "[!] tegrarcm attempt $tg_attempt failed (exit $tg_rc)." >&2
    # The boot ROM only answers one RCM session per power-on (it sends the
    # chip UID once).  Once it stops answering, retrying without a power
    # cycle just burns another USB timeout, so stop and ask for re-entry.
    if grep -q 'Resource temporarily unavailable' "$tg_out"; then
      tg_stale=1
      break
    fi
    tg_attempt=$((tg_attempt + 1))
    if [ "$tg_attempt" -le 3 ]; then
      sleep 3
    fi
  done
  unset LIBUSB_DEBUG
  rm -f "$tg_out"
  if [ "$tg_rc" -ne 0 ]; then
    if [ "$tg_stale" -eq 1 ]; then
      echo "[-] The oven stopped answering in RCM mode (tegrarcm: Resource temporarily unavailable)." >&2
      echo "[-] Unplug the oven power, wait 5 seconds, then hold SW2 and power it on again (keep holding ~10s), and retry." >&2
    else
      echo "[-] tegrarcm failed after 3 attempts (last exit $tg_rc)." >&2
      echo "[-] USB bus errors usually mean the cable or port: try another cable and a direct USB port (no hub), unplug/replug the oven, then retry." >&2
      echo "[-] Also make sure the oven is in RCM mode (hold SW2, power on, keep holding ~10s)." >&2
    fi
    return 1
  fi
  sleep 2
  echo "[*] Waiting for the June eMMC block device (up to 60s)..."
  for _ in $(seq 1 60); do
    DEV="$(find_june_disk || true)"
    if [ -n "$DEV" ] && [ -b "$DEV" ]; then
      echo "[*] target disk: $DEV (excluded boot/system disk: ${BOOT_OR_ROOT_DISK:-none})"
      return 0
    fi
    if [ $(( _ % 5 )) -eq 0 ]; then
      echo "[*] Still waiting for the June eMMC: $(( 60 - _ ))s remaining..."
    fi
    sleep 1
  done
  echo "[-] June eMMC block device never appeared." >&2
  return 1
}

usb_candidates() {
  local boot="$1" june="$2" b dev sz tran
  for b in /sys/block/sd*; do
    [ -e "$b" ] || continue
    dev="/dev/$(basename "$b")"
    [ -b "$dev" ] || continue
    [ "$dev" = "$boot" ] && continue
    [ "$dev" = "$june" ] && continue
    tran="$(lsblk -ndo TRAN "$dev" 2>/dev/null | awk 'NF{print $1}')"
    [ "$tran" = "usb" ] || continue
    sz="$(blockdev --getsize64 "$dev" 2>/dev/null || echo 0)"
    if [ "$sz" -ge 7800000000 ] && [ "$sz" != "$EXPECTED_SIZE" ]; then
      printf '%s\n' "$dev"
    fi
  done
}

pick_destination() {
  local boot="$1" june="$2" dest cand part mnt

  # Prefer the boot medium itself when it is already mounted read-write with
  # enough free space. This is the Ventoy case: the same USB stick carries both
  # the installer ISO and a normal writable data partition.
  dest="$(boot_writable_destination "$boot" || true)"
  if [ -n "$dest" ]; then
    printf '%s\n' "$dest"
    return 0
  fi

  dest="$(ventoy_data_partition || true)"
  if [ -n "$dest" ]; then
    printf '%s\n' "$dest"
    return 0
  fi

  for cand in $(usb_candidates "$boot" "$june"); do
    for part in /dev/$(basename "$cand")[0-9]*; do
      [ -b "$part" ] || continue
      [ "$part" = "$BOOT_SOURCE" ] && continue
      mnt="$(findmnt -rno TARGET "$part" 2>/dev/null || true)"
      case "$mnt" in
        /run/live/medium|/lib/live/mount/medium) continue ;;
      esac
      fstype="$(stat -f -c %T "$mnt" 2>/dev/null || true)"
      case "$fstype" in
        msdos|vfat)
          echo "[-] Skipping $mnt: FAT32 cannot hold the 7.3GB backup image." >&2
          continue
          ;;
      esac
      if [ -n "$mnt" ]; then
        ventoy_remount_rw "$mnt"
        if ! ventoy_write_probe "$mnt"; then
          echo "[-] Skipping $mnt: read-only." >&2
          continue
        fi
        avail="$(df -B1 --output=avail "$mnt" 2>/dev/null | tail -n1 | tr -d ' ' || echo 0)"
        if [ "${avail:-0}" -ge "$EXPECTED_SIZE" ]; then
          printf '%s\t%s\n' "$part" "$mnt"
          return 0
        fi
        continue
      fi
      mnt="/mnt/june-backup"
      mkdir -p "$mnt" 2>/dev/null || true
      if mountpoint -q "$mnt" 2>/dev/null; then
        umount "$mnt" 2>/dev/null || true
      fi
      if mount -o rw "$part" "$mnt" 2>/dev/null || mount "$part" "$mnt" 2>/dev/null; then
        ventoy_remount_rw "$mnt"
        if ! ventoy_write_probe "$mnt"; then
          echo "[-] Skipping $part: mounted read-only." >&2
          umount "$mnt" 2>/dev/null || true
          continue
        fi
        fstype="$(stat -f -c %T "$mnt" 2>/dev/null || true)"
        case "$fstype" in
          msdos|vfat)
            echo "[-] Skipping $mnt: FAT32 cannot hold the 7.3GB backup image." >&2
            umount "$mnt" 2>/dev/null || true
            continue
            ;;
        esac
        avail="$(df -B1 --output=avail "$mnt" 2>/dev/null | tail -n1 | tr -d ' ' || echo 0)"
        if [ "${avail:-0}" -ge "$EXPECTED_SIZE" ]; then
          printf '%s\t%s\n' "$part" "$mnt"
          return 0
        fi
        umount "$mnt" 2>/dev/null || true
        continue
      fi
    done
  done

  echo "[-] No writable USB destination found (need >= 8 GB)." >&2
  echo "[-] Use a spare USB drive, or boot from a Ventoy USB stick that has free space." >&2
  return 1
}

find_backup_images() {
  local boot="$1" june="$2" dest mnt
  dest="$(pick_destination "$boot" "$june" || true)"
  if [ -z "$dest" ]; then
    return 0
  fi
  mnt="${dest#*$'\t'}"
  find "$mnt" -maxdepth 2 -type f \( -name '*.img' -o -name '*.raw' \) -print 2>/dev/null | sort
}

BOOT_SOURCE="$(boot_media_source || true)"
BOOT_OR_ROOT_DISK="$(boot_or_root_disk || true)"

if [ "$MODE" = "list" ]; then
  list_destinations
  exit 0
fi

if [ "$MODE" = "ventoy-dir" ]; then
  ventoy_probe_line="$(ventoy_data_partition || true)"
  if [ -n "$ventoy_probe_line" ]; then
    printf '%s\n' "${ventoy_probe_line#*$'\t'}"
    exit 0
  fi
  exit 1
fi

sanitize_folder() {
  local raw="${1:-}" part out="" parts=()
  raw="${raw//\\//}"
  IFS='/' read -r -a parts <<< "$raw"
  for part in "${parts[@]}"; do
    part="${part#"${part%%[![:space:]]*}"}"
    part="${part%"${part##*[![:space:]]}"}"
    part="${part%"${part##*[!.]}"}"
    part="${part%"${part##*[![:space:]]}"}"
    [ -n "$part" ] || continue
    [ "$part" = "." ] && continue
    [ "$part" = ".." ] && continue
    out="${out:+$out/}$part"
  done
  printf '%s\n' "$out"
}

sanitize_filename() {
  local raw="${1:-}"
  raw="${raw//\\//}"
  raw="${raw##*/}"
  raw="${raw#"${raw%%[![:space:]]*}"}"
  raw="${raw%"${raw##*[![:space:]]}"}"
  raw="${raw%"${raw##*[!.]}"}"
  raw="${raw%"${raw##*[![:space:]]}"}"
  case "${raw,,}" in
    ""|*.img|*.raw) ;;
    *) raw="$raw.img" ;;
  esac
  printf '%s\n' "$raw"
}

# images DEST [FOLDER]: list restorable images as "<path>\t<bytes>" so a UI
# can let the user pick one (restore otherwise takes the first it finds).
if [ "$MODE" = "images" ]; then
  folder_arg="$(sanitize_folder "${3:-}")"
  dest="$(resolve_explicit_destination "${2:-}")" || exit 1
  root="${dest#*$'\t'}"
  [ -n "$folder_arg" ] && root="$root/$folder_arg"
  find "$root" -maxdepth 1 -type f \( -name '*.img' -o -name '*.raw' \) -printf '%p\t%s\n' 2>/dev/null | sort
  exit 0
fi

# verify_backup_image IMAGE: size + SHA256, no oven required. Prefers
# june-backup when the ISO payload includes it.
verify_backup_image() {
  local image="$1"
  if [ -z "$image" ] || [ ! -f "$image" ]; then
    echo "[-] Backup image not found: ${image:-(none)}" >&2
    echo "[-] Pick a .img file made by this installer." >&2
    return 1
  fi
  if [ -x "$DIR/june-backup" ]; then
    "$DIR/june-backup" verify -image "$image"
    return $?
  fi
  local sz want got
  sz="$(stat -c '%s' "$image" 2>/dev/null || echo 0)"
  if [ "$sz" != "$EXPECTED_SIZE" ]; then
    echo "[-] This backup file is the wrong size" >&2
    echo "[-] $image is $sz bytes; a June oven backup is $EXPECTED_SIZE bytes (about 7.8 GB)." >&2
    echo "[-] This is not a complete oven image. Do not restore it." >&2
    return 1
  fi
  if [ ! -f "$image.sha256" ]; then
    echo "[-] This backup has no SHA256 file" >&2
    echo "[-] Expected $(basename "$image").sha256 next to the image." >&2
    echo "[-] Refusing to restore an unverified backup. Make a new backup, or run: backup.sh checksum $image" >&2
    return 1
  fi
  want="$(awk '{print $1; exit}' "$image.sha256" | tr 'A-F' 'a-f')"
  echo "[*] Verifying backup with SHA256 (reads the whole image back; usually 1-3 minutes)..."
  got="$(sha256sum "$image" | awk '{print $1}')"
  if [ "$want" != "$got" ]; then
    echo "[-] The backup file does not match its SHA256" >&2
    echo "[-] Recorded: $want" >&2
    echo "[-] Computed: $got" >&2
    echo "[-] The file is damaged, incomplete, or was changed after it was made. Do not restore it." >&2
    return 1
  fi
  echo "[+] Backup image OK: $image ($sz bytes)"
  echo "[+] SHA256: $got"
}

write_backup_sidecar() {
  local image="$1" hash
  if [ -x "$DIR/june-backup" ]; then
    "$DIR/june-backup" checksum -image "$image"
    return $?
  fi
  hash="$(sha256sum "$image" | awk '{print $1}')"
  printf '%s  %s\n' "$hash" "$(basename "$image")" > "$image.sha256"
  echo "[+] SHA256 sidecar: $image.sha256"
  echo "[+] SHA256: $hash"
}

if [ "$MODE" = "verify" ]; then
  verify_backup_image "${2:-}" || exit 1
  exit 0
fi

if [ "$MODE" = "checksum" ]; then
  image="${2:-}"
  if [ -z "$image" ] || [ ! -f "$image" ]; then
    echo "[-] Backup image not found: ${image:-(none)}" >&2
    exit 1
  fi
  write_backup_sidecar "$image" || exit 1
  exit 0
fi

if [ "$MODE" = "restore-dry" ]; then
  image="${2:-}"
  if [ -x "$DIR/june-backup" ]; then
    if [ -n "${3:-}" ]; then
      "$DIR/june-backup" restore -image "$image" -device "$3" -dry-run || exit 1
    else
      "$DIR/june-backup" restore -image "$image" -dry-run || exit 1
    fi
  else
    echo "[*] Dry-run restore (nothing will be written)"
    verify_backup_image "$image" || exit 1
    echo "[+] Image is usable. Nothing was written."
  fi
  exit 0
fi

if [ -f "$DIR/BUILD_ID" ]; then
  echo "[*] Installer $(cat "$DIR/VERSION" 2>/dev/null || echo unknown), build: $(cat "$DIR/BUILD_ID")"
fi

wait_for_ums
JUNE_DISK="$DEV"
DEST_ARG="${2:-}"
FOLDER_ARG="$(sanitize_folder "${3:-}")"
FILENAME_ARG="$(sanitize_filename "${4:-}")"

unique_backup_name() {
  local dir="$1" name="$2" base ext n cand
  base="${name%.img}"
  ext=".img"
  cand="$name"
  n=1
  while [ -e "$dir/$cand" ]; do
    cand="${base}-${n}${ext}"
    n=$((n + 1))
  done
  printf '%s\n' "$cand"
}

case "$MODE" in
  backup)
    echo "[*] Preparing to back up the full June eMMC ($EXPECTED_SIZE bytes) to USB."
    echo "[*] This requires about 7.3 GB of free space."
    if [ -n "$DEST_ARG" ]; then
      dest="$(resolve_explicit_destination "$DEST_ARG")" || exit 1
    else
      dest="$(pick_destination "$BOOT_OR_ROOT_DISK" "$JUNE_DISK")"
    fi
    mnt="${dest#*$'\t'}"
    default_name="$(date +%y%m%d-%H%M).img"
    if [ -n "$FOLDER_ARG" ]; then
      mkdir -p "$mnt/$FOLDER_ARG"
      name="$(unique_backup_name "$mnt/$FOLDER_ARG" "${FILENAME_ARG:-$default_name}")"
      out="$mnt/$FOLDER_ARG/$name"
    else
      name="$(unique_backup_name "$mnt" "${FILENAME_ARG:-$default_name}")"
      out="$mnt/$name"
    fi
    avail="$(df -B1 --output=avail "$mnt" | tail -n1 | tr -d ' ')"
    if [ "${avail:-0}" -lt "$EXPECTED_SIZE" ]; then
      echo "[-] Not enough free space on $mnt: $avail bytes available, $EXPECTED_SIZE needed." >&2
      exit 1
    fi
    echo "[*] Backup size: 7.8 GB (7.3 GiB) - the whole June eMMC."
    echo "[*] USB write estimate: ~26 min at 5 MB/s, ~13 min at 10 MB/s, ~9 min at 15 MB/s."
    echo "[*] Our test stick ran at 9.6 MB/s: about 15 min including SHA256 verification."
    echo "[*] Writing backup image to: $out"
    dd if="$JUNE_DISK" of="$out" bs=4M conv=fsync status=progress
    echo "[*] Verifying backup with SHA256 (reads the whole image back; usually 1-3 minutes)..."
    write_backup_sidecar "$out" || exit 1
    echo "[+] Backup complete: $out"
    ;;
  restore)
    echo "[*] Looking for June backup images on USB..."
    if [ -n "$DEST_ARG" ]; then
      dest="$(resolve_explicit_destination "$DEST_ARG")" || exit 1
      mnt="${dest#*$'\t'}"
      root="$mnt"
      [ -n "$FOLDER_ARG" ] && root="$mnt/$FOLDER_ARG"
      images="$(find "$root" -maxdepth 1 -type f \( -name '*.img' -o -name '*.raw' \) -print 2>/dev/null | sort)"
    else
      images="$(find_backup_images "$BOOT_OR_ROOT_DISK" "$JUNE_DISK" || true)"
    fi
    if [ -z "$images" ]; then
      echo "[-] No .img/.raw backup found on the spare USB drive." >&2
      exit 1
    fi
    image="$(echo "$images" | head -n1)"
    # An explicit image name (4th argument) picks that file instead.
    if [ -n "$FILENAME_ARG" ] && [ -n "$DEST_ARG" ]; then
      image="$root/$(basename "$FILENAME_ARG")"
      if [ ! -f "$image" ]; then
        echo "[-] Backup image not found: $image" >&2
        exit 1
      fi
    fi
    # find_june_disk already matched the oven by size and skipped the boot
    # disk, so only the image needs checking here.
    echo "[*] Verifying backup image before writing..."
    verify_backup_image "$image" || exit 1
    echo "[*] WARNING: this overwrites the oven eMMC with $image"
    read -rp "Type RESTORE to continue: " confirm
    [ "$confirm" = "RESTORE" ] || { echo "[-] Aborted."; exit 1; }
    echo "[*] Restoring $image -> $JUNE_DISK"
    dd if="$image" of="$JUNE_DISK" bs=4M conv=fsync status=progress
    echo "[+] Restore complete."
    ;;
  *)
    echo "usage: backup.sh [backup|restore|verify|checksum|restore-dry]" >&2
    exit 2
    ;;
esac

echo
echo "Next: unplug the oven power, unplug USB, close/reassemble the oven, then"
echo "power it on normally."
echo "Guide: $GUIDE_URL"
EOF
chmod +x "$DEST/install.sh" "$DEST/tegrarcm" "$DEST/backup.sh"
BACKUP_SRC="$ARCH/installer/backup"
if [ -d "$BACKUP_SRC/cmd/june-backup" ] && command -v go >/dev/null 2>&1; then
  echo "[*] building june-backup"
  (cd "$BACKUP_SRC" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o "$SCRIPT_DIR/$DEST/june-backup" ./cmd/june-backup)
  chmod +x "$DEST/june-backup"
fi

echo "assets prepared in $DEST"
ls -l "$DEST"
