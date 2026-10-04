#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Paths default to this checkout but can be pointed anywhere, which keeps the
# payload build usable from a published/source-only tree. See PUBLISHING.md.
LR="${JUNE_LR:-$(cd "$SCRIPT_DIR/../.." && pwd)}"
ROOT="${JUNE_ROOT:-$(dirname "$LR")}"
# This repo is flat (liveiso/, onoven/, ...); the development tree nests the
# same directories under june-arch/. Support both with JUNE_LR.
if [ -d "$LR/june-arch/onoven" ]; then
  ARCH="$LR/june-arch"
else
  ARCH="$LR"
fi
DEST="assets"
rm -rf "$DEST"
mkdir -p "$DEST"

# Stamp every generated script with the build time so a log pasted back from
# the live system can be matched to the exact ISO that produced it.
date -u +%Y%m%dT%H%M%SZ > "$DEST/BUILD_ID"

cp "$ARCH/onoven/deploy_onoven.sh" "$DEST/"
cp "$ARCH/onoven/june_bootclear.sh.onoven" "$DEST/"
cp "$ARCH/onoven/june_reaper.sh.junelocal" "$DEST/"
cp "$ARCH/companions_state.json" "$DEST/"
cp "$ARCH/golocal/bin/june-local-linux-armv7" "$DEST/"
cp "$ARCH/golocal/bin/june-camerad-linux-armv7" "$DEST/"
cp "$ROOT/tools/certs/junelife_chain_godaddy_intermediate.crt" "$DEST/"
cp "$ROOT/tools/certs/june-local.key" "$DEST/"
cp "$LR/june-rcm/healthy-build/decrypted-priv-app/JuneCloudMessaging.apk" "$DEST/"
cp "$ARCH/windows-work/jcm-pinning/JuneCloudMessaging_nopin_signed.apk" "$DEST/"
cp "$ARCH/windows-work/remotes-pinning/Remotes_nopin_signed.apk" "$DEST/"
cp "$ARCH/windows-work/companion-endpoint/companion_endpoint_signed.apk" "$DEST/"
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

cat > "$DEST/install.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE="${1:-prepackaged}"
JCM_PERSIST=0
EXPECTED_SIZE="7818182656"
TEGRARCM="$DIR/tegrarcm"
GUIDE_URL="https://project-july.org/setup"
BUILD_ID="$(cat "$DIR/BUILD_ID" 2>/dev/null || echo unknown)"
echo "[*] Installer build: $BUILD_ID"

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
  OVEN_IP="${2:-}"
  OVEN_HOST="${3:-}"
  PERSIST="${4:-}"
  [ -n "$OVEN_IP" ] || read -rp "Oven LAN IP: " OVEN_IP
  [ -n "$OVEN_HOST" ] || read -rp "Hostname (optional): " OVEN_HOST
  OVEN_HOST="${OVEN_HOST:-oven.local}"
  export OVEN_HOST
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
  if [ "$PERSIST" = "persist" ]; then
    JCM_PERSIST=1
    export JCM_SRC="$DIR/JuneCloudMessaging_nopin_signed.apk"
  else
    export JCM_SRC="$DIR/JuneCloudMessaging.apk"
  fi
  # Prepackaged installs keep the GoDaddy chain, which the factory pins accept.
  export REMOTES_SRC=""
  export CERT_SRC="$DIR/junelife_chain_godaddy_intermediate.crt"
  export KEY_SRC="$DIR/june-local.key"
fi

echo "[debug] MODE=$MODE"
echo "[debug] JCM_PERSIST=$JCM_PERSIST"
echo "[debug] CERT_SRC=$(basename "$CERT_SRC")"
echo "[debug] JCM_SRC=$(basename "$JCM_SRC")"
sha256sum "$CERT_SRC" "$JCM_SRC"

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
  echo "  2. Set the endpoint to $OVEN_IP."
  echo "  3. Install the per-home CA on the phone: $DIR/certs/ca.crt"
  echo "  4. Open the June app and pair/control the oven."
else
  echo "Phone setup:"
  echo "  1. Install companion_endpoint_signed.apk."
  echo "  2. Set the endpoint to the oven's LAN IP."
  echo "  3. Open the June app and pair/control the oven."
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
    case "${label,,}" in ventoy) ;; *) continue ;; esac
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
          ventoy) echo "[diag] $dev ($label): '${fstype:-unknown}' cannot hold a >4 GB image." >&2 ;;
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
      ventoy)
        label="Ventoy (current boot USB)"
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

# images DEST [FOLDER]: list restorable images as "<path>\t<bytes>" so a UI
# can let the user pick one (restore otherwise takes the first it finds).
if [ "$MODE" = "images" ]; then
  dest="$(resolve_explicit_destination "${2:-}")" || exit 1
  root="${dest#*$'\t'}"
  [ -n "${3:-}" ] && root="$root/$3"
  find "$root" -maxdepth 1 -type f \( -name '*.img' -o -name '*.raw' \) -printf '%p\t%s\n' 2>/dev/null | sort
  exit 0
fi

if [ -f "$DIR/BUILD_ID" ]; then
  echo "[*] Installer build: $(cat "$DIR/BUILD_ID")"
fi

wait_for_ums
JUNE_DISK="$DEV"
DEST_ARG="${2:-}"
FOLDER_ARG="${3:-}"
FILENAME_ARG="${4:-}"

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
    sha256sum "$out" > "$out.sha256"
    echo "[+] Backup complete: $out"
    echo "[+] SHA256: $(cut -d' ' -f1 "$out.sha256")"
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
    sz="$(stat -c '%s' "$image" 2>/dev/null || echo 0)"
    if [ "$sz" != "$EXPECTED_SIZE" ]; then
      echo "[-] Backup size mismatch: $image is $sz bytes, expected $EXPECTED_SIZE." >&2
      exit 1
    fi
    echo "[*] WARNING: this overwrites the oven eMMC with $image"
    read -rp "Type RESTORE to continue: " confirm
    [ "$confirm" = "RESTORE" ] || { echo "[-] Aborted."; exit 1; }
    echo "[*] Restoring $image -> $JUNE_DISK"
    dd if="$image" of="$JUNE_DISK" bs=4M conv=fsync status=progress
    echo "[+] Restore complete."
    ;;
  *)
    echo "usage: backup.sh [backup|restore]" >&2
    exit 2
    ;;
esac

echo
echo "Next: unplug the oven power, unplug USB, close/reassemble the oven, then"
echo "power it on normally."
echo "Guide: $GUIDE_URL"
EOF
chmod +x "$DEST/install.sh" "$DEST/tegrarcm" "$DEST/backup.sh"

echo "assets prepared in $DEST"
ls -l "$DEST"
