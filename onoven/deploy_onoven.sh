#!/usr/bin/env bash
# Deploy the on-oven June local server (Go) over UMS.
#
# Run as root with the oven already in UMS mode (/dev/sda = the 7.3G eMMC block
# device). This script is deliberately paranoid: it refuses to run if /dev/sda
# is a regular file (the phantom-file trap that ate a dd earlier).
#
# What it installs:
#   /system/xbin/june-local          static ARMv7 Go server
#   /system/etc/june_leaf.crt        TLS chain (custom: per-home leaf+CA; basic: bundled project chain)
#   /system/etc/june_leaf.key        TLS leaf private key (0600)
#   /system/etc/security/cacerts/$CA_HASH.0
#                                    CA that signs the served chain, installed as
#                                    a trust anchor (custom: per-home CA; basic:
#                                    the project CA).  Without it a clean oven
#                                    rejects june-local's TLS certificate.
#   /system/etc/june_ca.crt          the same CA on its own, served at
#                                    /local/ca.crt for the phone to install
#   /system/etc/hosts                api/messaging -> 127.0.0.1
#   /data/june/media                 live-view JPEG directory
#   /data/june/logs                  server log + JSONL directory
#   /system/bin/june_bootclear.sh    force-adb only (no server launch)
#   /system/etc/june/jcm_nopin.apk or /data/june/jcm_nopin.apk
#       staged patched JCM for the post-boot jcm_apply service
#   /system/app/JuneDns/JuneDns.apk  June Local DNS (mDNS) helper app
#   /system/etc/june/identity/token  per-oven June account token (v2:..., 0644)
#   /system/etc/june/identity/accounts_seed.db
#       Android AccountManager DB seed; june_reaper installs it before Android
#       starts when /data has no June account row (factory-reset recovery)
#   /system/etc/june/identity/identity.json
#       copy of the /data identity record for the boot-time ensure
#   /data/june/identity.json         local oven identity record (0600; holds the token)
#   LNX/boot partition               boot_junelocal_init.img (dedicated init service)
#
# Optional env overrides:
#   CERT_SRC / KEY_SRC / CA_SRC / CA_HASH / BIN_SRC / JCM_SRC / BOOT_SRC
#   FLASH_BOOT=0   skip flashing the boot partition (LNX)
#   INSTALL_MODE=basic|custom and CA_MODE=project|per-home
#                  install.sh sets both; they drive install.json and which CA is
#                  trusted.  If omitted they are inferred from CA_SRC/CA_HASH.
#   JUNE_IDENTITY_GEN / JUNE_ACCOUNTS_TEMPLATE / PYTHON
#                  identity seed generator, schema template and interpreter
#                  (defaults: the files shipped in the payload, then python3)
#   DNS_SRC        June Local DNS helper APK (default: payload JuneDns_signed.apk)
set -euo pipefail

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SRC_DIR/../../.." && pwd)"

EXPECTED_SIZE="7818182656"

# Given a mounted source such as /dev/sda1, return the whole disk (/dev/sda).
disk_from_source() {
  local src="$1" base parent
  case "$src" in
    /dev/*) ;;
    *) return 1 ;;
  esac
  base="${src##*/}"

  # A partition source: walk its sysfs symlink back to the parent block device.
  if [ -e "/sys/class/block/$base/partition" ]; then
    parent="$(basename "$(dirname "$(readlink -f "/sys/class/block/$base")")")"
    [ -n "$parent" ] || return 1
    printf '/dev/%s\n' "$parent"
    return 0
  fi

  # Already a whole-disk device.
  if [ -b "$src" ] && [ -d "/sys/class/block/$base" ]; then
    printf '%s\n' "$src"
    return 0
  fi

  return 1
}

# The disk we booted from (or the disk holding the live medium) must never be
# treated as the oven.  This is the /sda phantom trap: on a live USB stick the
# boot media is often /dev/sda, while the oven appears as a later disk.
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

BOOT_OR_ROOT_DISK="$(boot_or_root_disk || true)"
DEV="${DEV:-$(find_june_disk || true)}"

BIN_SRC="${BIN_SRC:-$ROOT/local-resources/june-arch/golocal/bin/june-local-linux-armv7}"
CAMERAD_SRC="${CAMERAD_SRC:-$ROOT/local-resources/june-arch/golocal/bin/june-camerad-linux-armv7}"
CERT_SRC="${CERT_SRC:-$ROOT/tools/certs/junelife_chain_godaddy_intermediate.crt}"
KEY_SRC="${KEY_SRC:-$ROOT/tools/certs/june-local.key}"
CA_SRC="${CA_SRC:-}"
CA_HASH="${CA_HASH:-}"
JCM_SRC="${JCM_SRC:-$ROOT/local-resources/june-rcm/healthy-build/decrypted-priv-app/JuneCloudMessaging.apk}"
REMOTES_SRC="${REMOTES_SRC:-}"
MEDIACAPTURER_SRC="${MEDIACAPTURER_SRC:-}"
ANALYTICS_SRC="${ANALYTICS_SRC:-}"
CHEF_SRC="${CHEF_SRC:-}"
LIGHT_SRC="${LIGHT_SRC:-$SRC_DIR/OvenLight_signed.apk}"
DNS_SRC="${DNS_SRC:-$SRC_DIR/JuneDns_signed.apk}"
BOOTCLEAR_SRC="${BOOTCLEAR_SRC:-$SRC_DIR/june_bootclear.sh.onoven}"
JUNE_REAPER_SRC="${JUNE_REAPER_SRC:-$SRC_DIR/june_reaper.sh.junelocal}"
UPDATER_CONFIG_SRC="${UPDATER_CONFIG_SRC:-$SRC_DIR/updater.json}"
COMPANION_STATE_SRC="${COMPANION_STATE_SRC:-$ROOT/local-resources/june-arch/companions_state.json}"
BOOT_SRC="${BOOT_SRC:-$ROOT/local-resources/june-arch/tools/orchestrator/boot_junelocal_init.img}"
FLASH_BOOT="${FLASH_BOOT:-1}"
JCM_PERSIST="${JCM_PERSIST:-0}"
CHEF_PERSISTED=0
# Explicit provenance: install.sh exports INSTALL_MODE=basic|custom and
# CA_MODE=project|per-home instead of deploy guessing from CA_SRC/CA_HASH.
# Direct (dev/bench) invocations may omit them; they are resolved below.
INSTALL_MODE="${INSTALL_MODE:-}"
CA_MODE="${CA_MODE:-}"
# June Local per-oven identity: a fresh account token plus the Android
# AccountManager seed that june_reaper installs before Android starts when /data
# has no June account row.  The generator and schema template ship in the
# payload; PYTHON is only resolved when an identity actually has to be built.
JUNE_IDENTITY_GEN="${JUNE_IDENTITY_GEN:-$SRC_DIR/generate_identity.py}"
JUNE_ACCOUNTS_TEMPLATE="${JUNE_ACCOUNTS_TEMPLATE:-$SRC_DIR/accounts_template.db}"
PYTHON="${PYTHON:-}"
IDENTITY_TMP_DIR=""
IDENTITY_TOKEN=""
IDENTITY_SEED=""
IDENTITY_JSON=""
IDENTITY_SEED_SOURCE="unavailable"
ACCOUNT_PRESENT="false"
identity_token_sha=""

APP_OFF=$((92928 * 512))
UDA_OFF=$((8039680 * 512))
SLN_OFF=$((2755328 * 512))
SAP_OFF=$((2771712 * 512))
LNX_OFF=$((76544 * 512))
LNX_SIZE=$((16384 * 512))
SLN_SIZE=$((16384 * 512))

APP_MNT="/mnt/june_app"
UDA_MNT="/mnt/june_uda"
SAP_MNT="/mnt/june_sap"

require_block_device() {
  if [ -z "$DEV" ] || [ ! -b "$DEV" ]; then
    echo "[-] June eMMC block device not found. Put the oven in UMS mode first." >&2
    if [ -n "$BOOT_OR_ROOT_DISK" ]; then
      echo "[-] Excluded boot/system disk: $BOOT_OR_ROOT_DISK" >&2
    fi
    exit 1
  fi
  local sz
  sz="$(blockdev --getsize64 "$DEV" 2>/dev/null || echo 0)"
  if [ "$sz" != "$EXPECTED_SIZE" ]; then
    echo "[-] $DEV size is $sz, expected $EXPECTED_SIZE (June eMMC)." >&2
    echo "[-] Refusing to flash: this looks like the wrong disk." >&2
    exit 1
  fi
}

cleanup() {
  umount "$APP_MNT" "$UDA_MNT" "$SAP_MNT" 2>/dev/null || true
  losetup -d "${LOOP_APP:-}" "${LOOP_UDA:-}" "${LOOP_SAP:-}" 2>/dev/null || true
  if [ -n "${IDENTITY_TMP_DIR:-}" ]; then
    rm -rf "$IDENTITY_TMP_DIR"
  fi
}
trap cleanup EXIT

require_block_device
echo "[*] target disk: $DEV"
echo "[*] excluded boot/system disk: ${BOOT_OR_ROOT_DISK:-none}"

if [ ! -s "$UPDATER_CONFIG_SRC" ]; then
  echo "[-] updater configuration is missing: $UPDATER_CONFIG_SRC" >&2
  echo "    Refusing to install a server that cannot verify signed updates." >&2
  exit 1
fi

install_system_files() {
  local mnt="$1"
  install -m 0755 "$BIN_SRC" "$mnt/xbin/june-local"
  install -m 0755 "$CAMERAD_SRC" "$mnt/xbin/june-camerad"
  install -m 0644 "$CERT_SRC" "$mnt/etc/june_leaf.crt"
  install -m 0600 "$KEY_SRC" "$mnt/etc/june_leaf.key"
  if [ -n "$CA_SRC" ]; then
    # Served at /local/ca.crt as the CA alone (not the chain): custom installs
    # hand out the per-home CA and basic installs the bundled project CA, so the
    # phone can install either one as a trust anchor.
    install -m 0644 "$CA_SRC" "$mnt/etc/june_ca.crt"
  fi
  # The CA that signs the served chain must be trusted by the oven itself, in
  # basic installs too: basic serves a chain whose leaf is issued by the project
  # CA, which a clean factory oven does not trust.  Without this anchor the
  # stock June apps reject june-local's TLS handshake and june-local reports
  # "oven disconnected".  CA_MODE only labels the install in install.json; both
  # modes install the anchor whenever the caller provides CA_SRC and its
  # subject_hash_old.
  if [ -n "$CA_SRC" ] && [ -n "$CA_HASH" ]; then
    mkdir -p "$mnt/etc/security/cacerts"
    install -m 0644 "$CA_SRC" "$mnt/etc/security/cacerts/$CA_HASH.0"
    echo "[+] trusted CA installed on $mnt/etc/security/cacerts/$CA_HASH.0"
  fi
  install -m 0755 "$BOOTCLEAR_SRC" "$mnt/bin/june_bootclear.sh"
  # june_reaper.sh is the stock boot script that mounts the encrypted
  # /system/collector volume. The June Local copy mounts that same volume
  # read-write when a staged JCM patch exists and applies it before Android
  # starts; without a staged patch it behaves exactly like the stock script.
  install -m 0755 "$JUNE_REAPER_SRC" "$mnt/bin/june_reaper.sh"
  mkdir -p "$mnt/etc/june"
  install -m 0644 "$UPDATER_CONFIG_SRC" "$mnt/etc/june/updater.json"
  echo "[+] signed update repository config installed on $mnt/etc/june/updater.json"

  # Record the chosen hostname. The optional JuneDns helper app advertises
  # <name>.local over mDNS (it reads this file); the status page shows the name
  # and the supplied certificate covers june.local-style names for basic
  # installs.  If <name>.local does not resolve, use the oven IP.
  hostname_value="${OVEN_HOST:-june}"
  hostname_value="${hostname_value%.local}"
  [ -n "$hostname_value" ] || hostname_value="june"
  printf '%s\n' "$hostname_value" > "$mnt/etc/june_hostname"
  chmod 0644 "$mnt/etc/june_hostname"
  echo "[*] oven hostname: $hostname_value (use the oven IP if $hostname_value.local does not resolve)"

  # The live Android build deletes raw /system/priv-app at boot and mounts the
  # encrypted /system/collector EncFS volume over it, so writing the APK there
  # never affects the running package. The patched june_reaper.sh performs the
  # encrypted write during the first mount of that volume, before Android
  # starts; the boot image's one-shot `jcm_apply` service remains as a
  # fallback. When persistence is requested, the patched APK is staged in
  # /system so it is still available after a factory reset.
  case "$(basename "$JCM_SRC")" in
    JuneCloudMessaging_nopin_signed.apk)
      if [ "$JCM_PERSIST" = "1" ]; then
        mkdir -p "$mnt/etc/june"
        install -m 0644 "$JCM_SRC" "$mnt/etc/june/jcm_nopin.apk"
        jcm_dst_sha="$(sha256sum "$mnt/etc/june/jcm_nopin.apk" | awk '{print $1}')"
        jcm_src_sha="$(sha256sum "$JCM_SRC" | awk '{print $1}')"
        if [ "$jcm_dst_sha" != "$jcm_src_sha" ]; then
          echo "[-] persistent JCM APK write mismatch on $mnt" >&2
          echo "    src: $jcm_src_sha ($(stat -c '%s' "$JCM_SRC") bytes)" >&2
          echo "    dst: $jcm_dst_sha ($(stat -c '%s' "$mnt/etc/june/jcm_nopin.apk") bytes)" >&2
          exit 1
        fi
        echo "[+] persistent JCM patch staged on $mnt/etc/june/jcm_nopin.apk ($(stat -c '%s' "$JCM_SRC") bytes)"
      else
        rm -f "$mnt/etc/june/jcm_nopin.apk"
        if [ "${JCM_NOTICE_SHOWN:-0}" != "1" ]; then
          echo "[ ] JCM patch will be staged in /data (not persistent across factory reset)"
          JCM_NOTICE_SHOWN=1
        fi
      fi
      ;;
    *)
      # The prepackaged install uses the factory JCM. Never leave a factory
      # APK at the custom-patch staging path: jcm_apply would prefer it over
      # the real no-pin APK staged in /data.
      rm -f "$mnt/etc/june/jcm_nopin.apk"
      ;;
  esac

  # Remotes (the oven-side pairing UI) embeds the same pinned network client,
  # so a per-home CA only works once it is patched as well.
  if [ -n "$REMOTES_SRC" ]; then
    if [ "$JCM_PERSIST" = "1" ]; then
      mkdir -p "$mnt/etc/june"
      install -m 0644 "$REMOTES_SRC" "$mnt/etc/june/remotes_nopin.apk"
      rem_dst_sha="$(sha256sum "$mnt/etc/june/remotes_nopin.apk" | awk '{print $1}')"
      rem_src_sha="$(sha256sum "$REMOTES_SRC" | awk '{print $1}')"
      if [ "$rem_dst_sha" != "$rem_src_sha" ]; then
        echo "[-] persistent Remotes APK write mismatch on $mnt" >&2
        echo "    src: $rem_src_sha ($(stat -c '%s' "$REMOTES_SRC") bytes)" >&2
        echo "    dst: $rem_dst_sha ($(stat -c '%s' "$mnt/etc/june/remotes_nopin.apk") bytes)" >&2
        exit 1
      fi
      echo "[+] persistent Remotes patch staged on $mnt/etc/june/remotes_nopin.apk ($(stat -c '%s' "$REMOTES_SRC") bytes)"
    else
      rm -f "$mnt/etc/june/remotes_nopin.apk"
      echo "[ ] Remotes patch will be staged in /data (not persistent across factory reset)"
    fi
  else
    rm -f "$mnt/etc/june/remotes_nopin.apk"
  fi

  # MediaCapturer embeds the same pinned network client and uploads the
  # cook-time stills to June's cloud. With the pin neutralized and the
  # project CA trusted, those stills land on june-local instead, where
  # /1/videos/{id}/start_image rebroadcasts them as local 10011 frames.
  if [ -n "$MEDIACAPTURER_SRC" ]; then
    if [ "$JCM_PERSIST" = "1" ]; then
      mkdir -p "$mnt/etc/june"
      install -m 0644 "$MEDIACAPTURER_SRC" "$mnt/etc/june/mediacapturer_nopin.apk"
      mc_dst_sha="$(sha256sum "$mnt/etc/june/mediacapturer_nopin.apk" | awk '{print $1}')"
      mc_src_sha="$(sha256sum "$MEDIACAPTURER_SRC" | awk '{print $1}')"
      if [ "$mc_dst_sha" != "$mc_src_sha" ]; then
        echo "[-] persistent MediaCapturer APK write mismatch on $mnt" >&2
        echo "    src: $mc_src_sha ($(stat -c '%s' "$MEDIACAPTURER_SRC") bytes)" >&2
        echo "    dst: $mc_dst_sha ($(stat -c '%s' "$mnt/etc/june/mediacapturer_nopin.apk") bytes)" >&2
        exit 1
      fi
      echo "[+] persistent MediaCapturer patch staged on $mnt/etc/june/mediacapturer_nopin.apk ($(stat -c '%s' "$MEDIACAPTURER_SRC") bytes)"
    else
      rm -f "$mnt/etc/june/mediacapturer_nopin.apk"
      echo "[ ] MediaCapturer patch will be staged in /data (not persistent across factory reset)"
    fi
  else
    rm -f "$mnt/etc/june/mediacapturer_nopin.apk"
  fi

  # Chef (cook modes): widens the remote primitive whitelist and carries an
  # optional plan_index for toast levels and other multi-plan primitives. Chef
  # is a priv-app, so the APK is staged here and swapped into the collector
  # volume by june_reaper at boot. It is ~240 MB, so persist it only when the
  # partition has room; otherwise the /data staging below covers one boot.
  if [ -n "$CHEF_SRC" ] && [ "$JCM_PERSIST" = "1" ]; then
    chef_size="$(stat -c '%s' "$CHEF_SRC" 2>/dev/null || echo 0)"
    chef_need_kb=$(( (chef_size + 1023) / 1024 + 8192 ))
    chef_avail_kb="$(df -Pk "$mnt" 2>/dev/null | awk 'NR==2 {print $4}')"
    if [ "${chef_avail_kb:-0}" -ge "$chef_need_kb" ]; then
      mkdir -p "$mnt/etc/june"
      if install -m 0644 "$CHEF_SRC" "$mnt/etc/june/chef_cookmodes.apk"; then
        chef_dst_sha="$(sha256sum "$mnt/etc/june/chef_cookmodes.apk" | awk '{print $1}')"
        chef_src_sha="$(sha256sum "$CHEF_SRC" | awk '{print $1}')"
        if [ "$chef_dst_sha" = "$chef_src_sha" ]; then
          CHEF_PERSISTED=$((CHEF_PERSISTED + 1))
          echo "[+] persistent Chef patch staged on $mnt/etc/june/chef_cookmodes.apk ($chef_size bytes)"
        else
          echo "[!] persistent Chef patch write mismatch on $mnt; will stage in /data" >&2
          rm -f "$mnt/etc/june/chef_cookmodes.apk"
        fi
      else
        echo "[!] persistent Chef patch write failed on $mnt; will stage in /data" >&2
      fi
    else
      echo "[!] not enough space on $mnt for the Chef patch (need $(( chef_need_kb / 1024 )) MB, have $(( ${chef_avail_kb:-0} / 1024 )) MB); will stage in /data" >&2
    fi
  fi

  # Analytics is a plain /system/app package and pins the same June cert. It
  # is not used for cooking, so replacing it only stops the constant TLS
  # retry noise; it is safe to install directly.
  if [ -n "$ANALYTICS_SRC" ]; then
    if [ -f "$mnt/app/Analytics/Analytics.apk" ]; then
      install -m 0644 "$ANALYTICS_SRC" "$mnt/app/Analytics/Analytics.apk"
      an_dst_sha="$(sha256sum "$mnt/app/Analytics/Analytics.apk" | awk '{print $1}')"
      an_src_sha="$(sha256sum "$ANALYTICS_SRC" | awk '{print $1}')"
      if [ "$an_dst_sha" != "$an_src_sha" ]; then
        echo "[-] Analytics APK write mismatch on $mnt" >&2
        echo "    src: $an_src_sha" >&2
        echo "    dst: $an_dst_sha" >&2
        exit 1
      fi
      echo "[+] Analytics no-pin patch installed on $mnt/app/Analytics/Analytics.apk"
    else
      echo "[!] Analytics APK not found on $mnt; skipping Analytics patch" >&2
    fi
  fi

  # June Light Bridge: tiny exported service that june-local starts to drive
  # CookManager's AxonService lighting scenes while the camera is viewed.
  if [ -f "$LIGHT_SRC" ]; then
    mkdir -p "$mnt/app/JuneLight"
    install -m 0644 "$LIGHT_SRC" "$mnt/app/JuneLight/JuneLight.apk"
    light_dst_sha="$(sha256sum "$mnt/app/JuneLight/JuneLight.apk" | awk '{print $1}')"
    light_src_sha="$(sha256sum "$LIGHT_SRC" | awk '{print $1}')"
    if [ "$light_dst_sha" != "$light_src_sha" ]; then
      echo "[-] June Light Bridge APK write mismatch on $mnt" >&2
      echo "    src: $light_src_sha" >&2
      echo "    dst: $light_dst_sha" >&2
      exit 1
    fi
    echo "[+] June Light Bridge installed on $mnt/app/JuneLight/JuneLight.apk"
  else
    echo "[!] June Light Bridge APK not found at $LIGHT_SRC; interior light wake disabled" >&2
  fi

  # June Local DNS: tiny helper app that advertises <hostname>.local over mDNS.
  # Optional: a payload without the APK keeps an already-installed helper, so
  # reinstalling from an older stick never removes it.
  if [ -f "$DNS_SRC" ]; then
    mkdir -p "$mnt/app/JuneDns"
    install -m 0644 "$DNS_SRC" "$mnt/app/JuneDns/JuneDns.apk"
    dns_dst_sha="$(sha256sum "$mnt/app/JuneDns/JuneDns.apk" | awk '{print $1}')"
    dns_src_sha="$(sha256sum "$DNS_SRC" | awk '{print $1}')"
    if [ "$dns_dst_sha" != "$dns_src_sha" ]; then
      echo "[-] June Local DNS APK write mismatch on $mnt" >&2
      echo "    src: $dns_src_sha" >&2
      echo "    dst: $dns_dst_sha" >&2
      exit 1
    fi
    echo "[+] June Local DNS helper installed on $mnt/app/JuneDns/JuneDns.apk"
  elif [ -f "$mnt/app/JuneDns/JuneDns.apk" ]; then
    echo "[i] June Local DNS helper not in the payload; keeping the installed one"
  else
    echo "[!] June Local DNS helper APK not found at $DNS_SRC; <name>.local will not resolve" >&2
  fi

  # Per-oven June account seed (both APP and SAP: the SOS factory reset restores
  # APP from SAP, so a seed kept only in APP would be lost exactly when it is
  # needed).  june_reaper.sh copies the seed into /data before Android starts
  # when /data has no June account row; the token is the fallback credential for
  # the optional JCM token patch and for the bookkeeping identity record.
  if [ -n "$IDENTITY_TOKEN" ]; then
    mkdir -p "$mnt/etc/june/identity"
    printf '%s\n' "$IDENTITY_TOKEN" > "$mnt/etc/june/identity/token"
    chmod 0644 "$mnt/etc/june/identity/token"
    if [ -n "$IDENTITY_SEED" ] && [ -f "$IDENTITY_SEED" ]; then
      install -m 0644 "$IDENTITY_SEED" "$mnt/etc/june/identity/accounts_seed.db"
    fi
    if [ -n "$IDENTITY_JSON" ] && [ -f "$IDENTITY_JSON" ]; then
      # 0600: this record contains the full v2: token (a local secret); root
      # (deploy, june_reaper, june-local) is the only reader.
      install -m 0600 "$IDENTITY_JSON" "$mnt/etc/june/identity/identity.json"
    fi
    echo "[+] identity seed installed on $mnt/etc/june/identity (source: $IDENTITY_SEED_SOURCE)"
  fi

  if ! grep -qE '^127\.0\.0\.1[[:space:]]+api\.junelife\.com([[:space:]]|$)' "$mnt/etc/hosts"; then
    printf '\n127.0.0.1 api.junelife.com\n' >> "$mnt/etc/hosts"
  fi
  if ! grep -qE '^127\.0\.0\.1[[:space:]]+messaging\.junelife\.com([[:space:]]|$)' "$mnt/etc/hosts"; then
    printf '127.0.0.1 messaging.junelife.com\n' >> "$mnt/etc/hosts"
  fi

}

# --- June Local: per-oven identity seed -------------------------------------
# The June apps read one Android AccountManager account row from
# /data/system/users/0/accounts.db (type com.junelife.user, token "Full access").
# A factory reset wipes it, which drops the JCM's bearer token and leaves the
# oven "disconnected" even when the install itself is correct.  The installer
# mints a fresh token for this oven and builds accounts_seed.db from the schema
# template captured from a real oven; june_reaper.sh copies the seed into /data
# before Android starts when the June row is missing.  Only the captured
# schema/meta are used: the captured cloud token never ships in the ISO.
find_python() {
  local candidate
  if [ -n "$PYTHON" ] && command -v "$PYTHON" >/dev/null 2>&1; then
    return 0
  fi
  for candidate in python3 python; do
    if command -v "$candidate" >/dev/null 2>&1; then
      PYTHON="$candidate"
      return 0
    fi
  done
  PYTHON=""
  return 1
}

# Write the oven identity record (docs/identity-generation.md §4.3) to
# $IDENTITY_JSON.  The full token lives here and in /system; install.json only
# ever gets its hash prefix.
write_identity_json() {
  local endpoint_host api_url messaging_url
  endpoint_host="$install_ip"
  if [ -z "$endpoint_host" ]; then
    endpoint_host="${OVEN_HOST:-june.local}"
    case "$endpoint_host" in
      *.*) ;;
      *) endpoint_host="$endpoint_host.local" ;;
    esac
  fi
  api_url="https://$endpoint_host"
  messaging_url="wss://$endpoint_host"
  IDENTITY_JSON="$IDENTITY_TMP_DIR/identity.json"
  cat > "$IDENTITY_JSON" <<EOF
{
  "version": 1,
  "role": "oven",
  "generated_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "generated_by": "june-local-installer/$installer_version",
  "endpoint": { "api": "$api_url", "messaging": "$messaging_url" },
  "account": {
    "name": "June Oven",
    "type": "com.junelife.user",
    "token_type": "Full access",
    "refresh_token_type": "Refresh access",
    "token": "$IDENTITY_TOKEN"
  },
  "oven": {
    "device_id": null,
    "model": "meerkat",
    "serial": null
  },
  "notes": "token minted locally at install time; the local relay accepts any bearer token; the Ed25519 signing key is in the ATECC508A and survives a reset"
}
EOF
  # 0600: identity.json carries the full v2: token (a local secret).  Only root
  # programs read it (deploy, june_reaper, june-local).
  chmod 0600 "$IDENTITY_JSON"
}

# Resolve the token and build the seed before the first system partition is
# written, reusing an identity a previous June Local install put in /system.
# $1 = the mounted APP system partition.
prepare_identity_assets() {
  local mnt="$1" seed_dir existing_token have_seed
  seed_dir="$mnt/etc/june/identity"
  IDENTITY_TMP_DIR="$(mktemp -d /tmp/june-identity.XXXXXX)"
  existing_token=""
  if [ -f "$seed_dir/token" ]; then
    existing_token="$(cat "$seed_dir/token" 2>/dev/null || true)"
  fi
  have_seed=0
  if [ -f "$seed_dir/accounts_seed.db" ]; then
    have_seed=1
  fi
  case "$existing_token" in
    v2:*) IDENTITY_TOKEN="$existing_token" ;;
  esac
  if [ -n "$IDENTITY_TOKEN" ] && [ "$have_seed" = "1" ]; then
    IDENTITY_SEED="$seed_dir/accounts_seed.db"
    IDENTITY_SEED_SOURCE="preserved"
    # Work from a temp copy: the source lives on the APP partition, which is
    # unmounted again before /data is written, and install(1) must not copy the
    # seed onto itself.  If the copy fails, keep the installed seed as-is.
    if cp -f "$IDENTITY_SEED" "$IDENTITY_TMP_DIR/accounts_seed.db" 2>/dev/null; then
      IDENTITY_SEED="$IDENTITY_TMP_DIR/accounts_seed.db"
    else
      IDENTITY_SEED=""
    fi
    echo "[*] identity: reusing the token + seed already installed in /system"
  else
    if ! find_python; then
      echo "[!] identity: no python3 interpreter found; the per-oven account seed is NOT installed" >&2
      echo "[!] identity: the oven will depend on an existing /data account row until this is fixed" >&2
      IDENTITY_TOKEN=""
      IDENTITY_SEED_SOURCE="unavailable"
      return 0
    fi
    if [ -z "$IDENTITY_TOKEN" ]; then
      if ! IDENTITY_TOKEN="$("$PYTHON" "$JUNE_IDENTITY_GEN" mint-token 2>/dev/null)"; then
        echo "[!] identity: could not mint a token with $JUNE_IDENTITY_GEN" >&2
        IDENTITY_TOKEN=""
        IDENTITY_SEED_SOURCE="unavailable"
        return 0
      fi
    fi
    if [ ! -f "$JUNE_ACCOUNTS_TEMPLATE" ] || [ ! -f "$JUNE_IDENTITY_GEN" ]; then
      echo "[!] identity: accounts template/generator missing from the payload" >&2
      echo "[!] identity: expected $JUNE_ACCOUNTS_TEMPLATE and $JUNE_IDENTITY_GEN" >&2
      echo "[!] identity: the token is installed but boot-time account self-heal is unavailable" >&2
      IDENTITY_SEED_SOURCE="unavailable"
    else
      IDENTITY_SEED="$IDENTITY_TMP_DIR/accounts_seed.db"
      if "$PYTHON" "$JUNE_IDENTITY_GEN" make-seed "$JUNE_ACCOUNTS_TEMPLATE" "$IDENTITY_SEED" "$IDENTITY_TOKEN"; then
        IDENTITY_SEED_SOURCE="generated"
      else
        echo "[!] identity: could not build accounts_seed.db from the template" >&2
        echo "[!] identity: boot-time account self-heal is unavailable on this install" >&2
        IDENTITY_SEED=""
        IDENTITY_SEED_SOURCE="unavailable"
      fi
    fi
  fi
  identity_token_sha="$(printf '%s' "$IDENTITY_TOKEN" | sha256sum | awk '{print substr($1,1,8)}')"
  write_identity_json
  echo "[*] identity: token sha256 prefix ${identity_token_sha:-none}, seed source $IDENTITY_SEED_SOURCE"
}

# Basic/prepackaged installs serve the bundled project chain, whose leaf is
# issued by the project's own CN=*.junelife.com CA.  A clean factory oven does
# not trust that CA, so the install itself must add it to the oven's trust
# store (install_system_files does) or the oven rejects june-local's TLS
# certificate after the deploy "succeeds" and never opens its socket.  Fail
# before writing anything if the payload cannot supply a usable trust anchor:
# basic mode requires CA_SRC + CA_HASH, CA_SRC must really be a CA certificate
# and CA_HASH must be its subject_hash_old.  Custom/per-home installs generate
# their CA at install time (CA_MODE=per-home) and are not checked here.
preflight_basic_trust_anchor() {
  local ca_derived ca_text
  [ "$install_mode" = "basic" ] || return 0
  if [ -z "$CA_SRC" ] || [ -z "$CA_HASH" ]; then
    echo "[-] Refusing basic install: no CA trust anchor to install." >&2
    echo "[-] Basic installs serve the project chain, whose leaf is issued by the" >&2
    echo "[-] project CN=*.junelife.com CA, and a clean oven does not trust that CA." >&2
    echo "[-] The anchor is required or the stock June apps reject june-local's TLS" >&2
    echo "[-] certificate and the oven never connects.  The caller must export CA_SRC" >&2
    echo "[-] (the project CA) and CA_HASH (its openssl x509 -subject_hash_old)." >&2
    echo "[-] install.sh does this; a direct dev/bench invocation must pass both." >&2
    echo "[-] Stopping here; the /system and /data partitions were not modified." >&2
    exit 1
  fi
  if ! command -v openssl >/dev/null 2>&1; then
    echo "[-] Refusing basic install: openssl is required to verify the CA trust anchor." >&2
    echo "[-] Stopping here; the /system and /data partitions were not modified." >&2
    exit 1
  fi
  if ! ca_text="$(openssl x509 -in "$CA_SRC" -noout -text 2>/dev/null)"; then
    echo "[-] Refusing basic install: CA_SRC is not a readable X.509 certificate." >&2
    echo "[-] CA_SRC: $CA_SRC" >&2
    echo "[-] Stopping here; the /system and /data partitions were not modified." >&2
    exit 1
  fi
  case "$ca_text" in
    *"CA:TRUE"*) ;;
    *)
      echo "[-] Refusing basic install: CA_SRC is not a CA certificate." >&2
      echo "[-] CA_SRC: $CA_SRC (Basic Constraints CA:TRUE is absent)" >&2
      echo "[-] Stopping here; the /system and /data partitions were not modified." >&2
      exit 1
      ;;
  esac
  ca_derived="$(openssl x509 -in "$CA_SRC" -subject_hash_old -noout 2>/dev/null || true)"
  if [ "$ca_derived" != "$CA_HASH" ]; then
    echo "[-] Refusing basic install: CA_HASH does not match CA_SRC." >&2
    echo "[-] CA_SRC:   $CA_SRC" >&2
    echo "[-] CA_HASH:  $CA_HASH (from caller)" >&2
    echo "[-] computed: ${ca_derived:-<unreadable>} (openssl x509 -subject_hash_old)" >&2
    echo "[-] The anchor would be written under the wrong name and the oven would" >&2
    echo "[-] not trust june-local's certificate.  Stopping here; the /system and /data partitions were not modified." >&2
    exit 1
  fi
}

required_sources=("$BIN_SRC" "$CAMERAD_SRC" "$CERT_SRC" "$KEY_SRC" "$JCM_SRC" "$BOOTCLEAR_SRC" "$JUNE_REAPER_SRC" "$COMPANION_STATE_SRC")
required_sources+=("$LIGHT_SRC")
if [ -n "$REMOTES_SRC" ]; then
  required_sources+=("$REMOTES_SRC")
fi
if [ -n "$MEDIACAPTURER_SRC" ]; then
  required_sources+=("$MEDIACAPTURER_SRC")
fi
if [ -n "$ANALYTICS_SRC" ]; then
  required_sources+=("$ANALYTICS_SRC")
fi
if [ -n "$CHEF_SRC" ]; then
  required_sources+=("$CHEF_SRC")
fi
if [ -n "$CA_SRC" ]; then
  required_sources+=("$CA_SRC")
fi
if [ "$FLASH_BOOT" = "1" ]; then
  required_sources+=("$BOOT_SRC")
fi
for f in "${required_sources[@]}"; do
  [ -f "$f" ] || { echo "[-] missing source file: $f" >&2; exit 1; }
done

echo "[*] binary: $BIN_SRC"
echo "[*] camerad: $CAMERAD_SRC"
echo "[*] cert:   $CERT_SRC"
echo "[*] key:    $KEY_SRC"
echo "[*] jcm:    $JCM_SRC"
if [ -n "$REMOTES_SRC" ]; then
  echo "[*] remotes: $REMOTES_SRC"
fi
if [ -n "$MEDIACAPTURER_SRC" ]; then
  echo "[*] mediacapturer: $MEDIACAPTURER_SRC"
fi
if [ -n "$ANALYTICS_SRC" ]; then
  echo "[*] analytics: $ANALYTICS_SRC"
fi
if [ -n "$CHEF_SRC" ]; then
  echo "[*] chef: $CHEF_SRC ($(stat -c '%s' "$CHEF_SRC") bytes)"
else
  echo "[i] Chef cook-modes patch not staged (optional)"
fi
echo "[*] light: $LIGHT_SRC"
if [ -f "$DNS_SRC" ]; then
  echo "[*] june-dns: $DNS_SRC"
else
  echo "[i] June Local DNS helper not in the payload (optional; <name>.local needs it)"
fi
echo "[*] jcm persistence: $JCM_PERSIST (1 = /system, 0 = /data)"
echo "[*] companions: $COMPANION_STATE_SRC"
echo "[*] boot:   $BOOT_SRC (flash=$FLASH_BOOT)"

# --- install provenance (must be resolved before install_system_files runs) ---
# install.sh exports INSTALL_MODE=basic|custom and CA_MODE=project|per-home.
# Older or direct invocations may omit them: infer the historical behaviour and
# say so instead of silently mislabelling the install in install.json.
if [ -z "$INSTALL_MODE" ]; then
  if [ -n "$CA_SRC" ]; then
    INSTALL_MODE="custom"
  else
    INSTALL_MODE="basic"
  fi
  echo "[!] INSTALL_MODE not set; inferred '$INSTALL_MODE' from CA_SRC (callers should export INSTALL_MODE)" >&2
fi
if [ -z "$CA_MODE" ]; then
  if [ "$INSTALL_MODE" = "custom" ]; then
    CA_MODE="per-home"
  else
    CA_MODE="project"
  fi
  echo "[!] CA_MODE not set; inferred '$CA_MODE' from INSTALL_MODE (callers should export CA_MODE)" >&2
fi
case "$INSTALL_MODE" in
  basic|custom) ;;
  *)
    echo "[-] INSTALL_MODE must be basic or custom, got '$INSTALL_MODE'" >&2
    exit 1
    ;;
esac
case "$CA_MODE" in
  project|per-home) ;;
  *)
    echo "[-] CA_MODE must be project or per-home, got '$CA_MODE'" >&2
    exit 1
    ;;
esac
install_mode="$INSTALL_MODE"
ca_mode="$CA_MODE"
if [ "$ca_mode" = "per-home" ] && [ -z "$CA_HASH" ]; then
  echo "[!] CA_MODE=per-home but CA_HASH is empty: $CA_SRC will be served from /local/ca.crt" >&2
  echo "[!] but NOT added to the oven's trust store, so the per-home CA will not validate." >&2
fi
if [ "$install_mode" = "basic" ] && [ "$ca_mode" != "project" ]; then
  echo "[!] install_mode=basic with ca_mode=$ca_mode is unusual; basic installs expect ca_mode=project." >&2
fi
echo "[*] install mode: $install_mode (ca_mode: $ca_mode)"

# Values shared by the identity record and install.json, resolved before the
# first system partition is written.
installer_version="${JUNE_VERSION:-$(cat "$SRC_DIR/VERSION" 2>/dev/null || echo 0.8)}"
build_id="${JUNE_BUILD_ID:-$(cat "$SRC_DIR/BUILD_ID" 2>/dev/null || echo unknown)}"
install_ip="${JUNE_OVEN_IP:-}"

# --- LNX/boot partition ---
if [ "$FLASH_BOOT" = "1" ]; then
  boot_size="$(stat -c '%s' "$BOOT_SRC")"
  if [ "$boot_size" -gt "$LNX_SIZE" ]; then
    echo "[-] boot image ($boot_size) exceeds LNX partition ($LNX_SIZE)" >&2
    exit 1
  fi
  echo "[*] flashing boot image to LNX (seek=76544)"
  dd if="$BOOT_SRC" of="$DEV" bs=512 seek=76544 conv=fsync status=none
  echo "[*] flashing boot image to SLN (seek=2755328)"
  dd if="$BOOT_SRC" of="$DEV" bs=512 seek=2755328 conv=fsync status=none
  sync
  echo "[+] boot image flashed to LNX + SLN ($boot_size bytes)"
fi

# --- APP/system ---
LOOP_APP="$(losetup -f --show -o "$APP_OFF" "$DEV")"
mkdir -p "$APP_MNT"
mount -t ext4 -o rw "$LOOP_APP" "$APP_MNT"
# Fail before writing to the oven when this basic install cannot install the
# trust anchor that makes the project chain validate (a clean oven would reject
# june-local's TLS certificate afterwards).  Custom/per-home installs are
# exempt: they generate their CA and hash at install time.
preflight_basic_trust_anchor
# Mint/collect the per-oven identity and build the account seed before the
# first system partition is written: the seed is installed on APP and SAP.
prepare_identity_assets "$APP_MNT"
install_system_files "$APP_MNT"

sync
umount "$APP_MNT"
losetup -d "$LOOP_APP"
LOOP_APP=""

# --- SAP/factory fallback system ---
LOOP_SAP="$(losetup -f --show -o "$SAP_OFF" "$DEV")"
mkdir -p "$SAP_MNT"
if ! mount -t ext4 -o rw "$LOOP_SAP" "$SAP_MNT"; then
  echo "[!] SAP rw mount failed; running e2fsck -f then retrying"
  e2fsck -f -y "$LOOP_SAP" || true
  mount -t ext4 -o rw "$LOOP_SAP" "$SAP_MNT"
fi
install_system_files "$SAP_MNT"

sync
umount "$SAP_MNT"
losetup -d "$LOOP_SAP"
LOOP_SAP=""

# --- UDA/data ---
LOOP_UDA="$(losetup -f --show -o "$UDA_OFF" "$DEV")"
mkdir -p "$UDA_MNT"
if ! mount -t ext4 -o rw "$LOOP_UDA" "$UDA_MNT"; then
  echo "[!] /data rw mount failed; running e2fsck -f then retrying"
  e2fsck -f -y "$LOOP_UDA" || true
  mount -t ext4 -o rw "$LOOP_UDA" "$UDA_MNT"
fi

mkdir -p "$UDA_MNT/june/media" "$UDA_MNT/june/logs" "$UDA_MNT/june/updates/transactions"
chmod 0755 "$UDA_MNT/june" "$UDA_MNT/june/media" "$UDA_MNT/june/logs" \
  "$UDA_MNT/june/updates" "$UDA_MNT/june/updates/transactions"

# This per-install secret authorizes check/stage actions on the local status
# page. It is separate from the release signing key: compromising one oven's
# token cannot sign a malicious update for anybody.
update_token_file="$UDA_MNT/june/updates/control-token"
update_token=""
if [ -s "$update_token_file" ]; then
  update_token="$(cat "$update_token_file" 2>/dev/null || true)"
fi
if [[ "$update_token" =~ ^[0-9a-f]{64}$ ]]; then
  chmod 0600 "$update_token_file"
  echo "[+] preserving existing wireless update control token"
else
  if command -v openssl >/dev/null 2>&1; then
    update_token="$(openssl rand -hex 32)"
  else
    update_token="$(dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')"
  fi
  if ! [[ "$update_token" =~ ^[0-9a-f]{64}$ ]]; then
    echo "[-] could not generate a wireless update control token" >&2
    exit 1
  fi
  printf '%s\n' "$update_token" > "$update_token_file"
  chmod 0600 "$update_token_file"
  echo "[+] wireless update control token: $update_token"
  echo "    Enter this token in /local → Software updates. Store it with the oven records."
fi

# Make the token easy to use from the installer and the wireless updater
# client without reading /data over ADB. It stays out of system images and is
# never included in the public release signing material.
update_token_value="$(cat "$update_token_file" 2>/dev/null)"
if [ -n "$update_token_value" ]; then
  if mkdir -p /run/june-installer && \
     install -m 0600 "$update_token_file" /run/june-installer/update-token; then
    echo "[+] wireless update token exported for the installer UI"
  else
    echo "[!] could not export update token to /run; installation will continue" >&2
  fi
  if [ -d /media/ventoy ]; then
    if mkdir -p /media/ventoy/june-logs && \
       install -m 0600 "$update_token_file" /media/ventoy/june-logs/update-token.txt; then
      echo "[+] wireless update token saved to the boot USB: /june-logs/update-token.txt"
      # The same password as a QR code that opens /local with it filled in. The
      # password sits in the #fragment, which browsers never send to the oven.
      qr_host="${install_ip:-${OVEN_HOST:-june}.local}"
      qr_host="${qr_host%.local.local}"
      if command -v qrencode >/dev/null 2>&1 && \
         qrencode -t SVG -m 2 -l M -o /media/ventoy/june-logs/update-password-qr.svg \
           "https://${qr_host}/local#updates&pw=${update_token_value}" 2>/dev/null; then
        chmod 0600 /media/ventoy/june-logs/update-password-qr.svg 2>/dev/null || true
        echo "[+] update password QR saved to the boot USB: /june-logs/update-password-qr.svg"
      fi
    else
      echo "[!] could not save update token to the boot USB; installation will continue" >&2
    fi
  fi
fi

# Preserve an existing companion registry: a normal reinstall must never force
# clients to re-pair. The payload file only seeds a fresh /data.
if [ -s "$UDA_MNT/june/companions_state.json" ]; then
  reg_backup="$UDA_MNT/june/companions_state.json.preinstall-$(date +%Y%m%d-%H%M%S)"
  cp -f "$UDA_MNT/june/companions_state.json" "$reg_backup" 2>/dev/null || true
  reg_size="$(stat -c '%s' "$UDA_MNT/june/companions_state.json" 2>/dev/null || echo 0)"
  echo "[+] preserving existing companion registry ($reg_size bytes; backup $(basename "$reg_backup"))"
else
  install -m 0644 "$COMPANION_STATE_SRC" "$UDA_MNT/june/companions_state.json"
  echo "[+] seeded companion registry from payload"
fi

# --- June Local: per-oven identity on /data ---------------------------------
# /data/june/identity.json is the operator/status record (not the gate); keep an
# existing one so a reinstall does not rewrite it.
if [ -f "$UDA_MNT/june/identity.json" ]; then
  echo "[+] identity: preserving existing /data/june/identity.json"
elif [ -n "$IDENTITY_JSON" ] && [ -f "$IDENTITY_JSON" ]; then
  # 0600: the identity record contains the full v2: token.
  install -m 0600 "$IDENTITY_JSON" "$UDA_MNT/june/identity.json"
  echo "[+] identity: wrote /data/june/identity.json (0600)"
fi
# Preserve an existing June account row: the oven may already be paired and
# using its own token.  Only a /data without any June row gets the seed here;
# the boot-time ensure is the authority for every other case.
accounts_db="$UDA_MNT/system/users/0/accounts.db"
if [ -f "$accounts_db" ]; then
  if grep -aq 'com\.junelife\.user' "$accounts_db" 2>/dev/null; then
    ACCOUNT_PRESENT="true"
    echo "[+] identity: existing June account row preserved (left untouched)"
  else
    echo "[*] identity: /data accounts.db has no June row; the boot ensure will back it up and install the seed"
  fi
elif [ -n "$IDENTITY_SEED" ] && [ -f "$IDENTITY_SEED" ]; then
  # Fresh/reset /data: seed the row now as well, so the very first boot works
  # even if the boot ensure cannot run; the ensure re-checks before Android.
  mkdir -p "$UDA_MNT/system/users/0"
  install -m 0600 "$IDENTITY_SEED" "$accounts_db"
  if ! chown 1000:1000 "$accounts_db" 2>/dev/null; then
    echo "[!] identity: could not chown $accounts_db to 1000:1000" >&2
  fi
  rm -f "$accounts_db-journal" "$accounts_db-wal" "$accounts_db-shm"
  sync
  ACCOUNT_PRESENT="true"
  echo "[+] identity: seeded /data/system/users/0/accounts.db from the installer"
fi

# Provenance for the status page and support: what was installed, how, and
# the expected hashes of the components june-local reports on.
comp_sha() {
  [ -n "${1:-}" ] && [ -f "$1" ] && sha256sum "$1" | awk '{print $1}' || true
}
case "$(basename "$JCM_SRC")" in
  JuneCloudMessaging_nopin_signed.apk) jcm_sha="$(comp_sha "$JCM_SRC")" ;;
  *) jcm_sha="" ;;
esac
# install_mode/ca_mode were resolved and validated before the boot/APP writes;
# they are used verbatim here so basic installs are no longer mislabelled as
# custom just because CA_SRC is set for the /local/ca.crt download.
persist="off"
[ "$JCM_PERSIST" = "1" ] && persist="on"
cat > "$UDA_MNT/june/install.json" <<EOF
{
  "mode": "$install_mode",
  "date": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "installer_version": "$installer_version",
  "build_id": "$build_id",
  "persistence": "$persist",
  "ca_mode": "$ca_mode",
  "hostname": "${hostname_value:-june}",
  "install_ip": "$install_ip",
  "identity": {
    "account_present": $ACCOUNT_PRESENT,
    "seed_source": "$IDENTITY_SEED_SOURCE",
    "token_sha256_prefix": "$identity_token_sha"
  },
  "components": {
    "june-local": "$(comp_sha "$BIN_SRC")",
    "june-camerad": "$(comp_sha "$CAMERAD_SRC")",
    "OvenLight": "$(comp_sha "$LIGHT_SRC")",
    "JuneDns": "$(comp_sha "$DNS_SRC")",
    "Chef": "$(comp_sha "$CHEF_SRC")",
    "JuneCloudMessaging": "$jcm_sha",
    "Remotes": "$(comp_sha "$REMOTES_SRC")",
    "MediaCapturer": "$(comp_sha "$MEDIACAPTURER_SRC")",
    "Analytics": "$(comp_sha "$ANALYTICS_SRC")"
  }
}
EOF
chmod 0644 "$UDA_MNT/june/install.json"
echo "[+] wrote /data/june/install.json ($install_mode, ca_mode $ca_mode, persistence $persist)"
echo "[+] identity: account_present=$ACCOUNT_PRESENT seed_source=$IDENTITY_SEED_SOURCE token_sha256_prefix=${identity_token_sha:-none}"

case "$(basename "$JCM_SRC")" in
  JuneCloudMessaging_nopin_signed.apk)
    if [ "$JCM_PERSIST" = "1" ]; then
      echo "[ ] persistent JCM patch staged in /system/etc/june/jcm_nopin.apk"
    else
      install -m 0644 "$JCM_SRC" "$UDA_MNT/june/jcm_nopin.apk"
      jcm_dst_sha="$(sha256sum "$UDA_MNT/june/jcm_nopin.apk" | awk '{print $1}')"
      jcm_src_sha="$(sha256sum "$JCM_SRC" | awk '{print $1}')"
      if [ "$jcm_dst_sha" != "$jcm_src_sha" ]; then
        echo "[-] /data JCM APK write mismatch" >&2
        echo "    src: $jcm_src_sha" >&2
        echo "    dst: $jcm_dst_sha" >&2
        exit 1
      fi
      echo "[+] non-persistent JCM patch staged in /data/june/jcm_nopin.apk ($(stat -c '%s' "$JCM_SRC") bytes)"
    fi
    ;;
  *)
    # Standard install: the factory JuneCloudMessaging app is used as-is.
    # Remove any payload left behind by an earlier patched install so the
    # post-boot applier cannot resurrect a stale patch (and never write the
    # factory APK under the patch name).
    rm -f "$UDA_MNT/june/jcm_nopin.apk"
    echo "[*] standard install keeps the factory JuneCloudMessaging app (no JCM patch staged)"
    ;;
esac

if [ -n "$REMOTES_SRC" ]; then
  if [ "$JCM_PERSIST" != "1" ]; then
    install -m 0644 "$REMOTES_SRC" "$UDA_MNT/june/remotes_nopin.apk"
    rem_dst_sha="$(sha256sum "$UDA_MNT/june/remotes_nopin.apk" | awk '{print $1}')"
    rem_src_sha="$(sha256sum "$REMOTES_SRC" | awk '{print $1}')"
    if [ "$rem_dst_sha" != "$rem_src_sha" ]; then
      echo "[-] /data Remotes APK write mismatch" >&2
      echo "    src: $rem_src_sha" >&2
      echo "    dst: $rem_dst_sha" >&2
      exit 1
    fi
    echo "[+] non-persistent Remotes patch staged in /data/june/remotes_nopin.apk ($(stat -c '%s' "$REMOTES_SRC") bytes)"
  else
    echo "[ ] persistent Remotes patch staged in /system/etc/june/remotes_nopin.apk"
  fi
else
  rm -f "$UDA_MNT/june/remotes_nopin.apk"
fi

if [ -n "$MEDIACAPTURER_SRC" ]; then
  if [ "$JCM_PERSIST" != "1" ]; then
    install -m 0644 "$MEDIACAPTURER_SRC" "$UDA_MNT/june/mediacapturer_nopin.apk"
    mc_dst_sha="$(sha256sum "$UDA_MNT/june/mediacapturer_nopin.apk" | awk '{print $1}')"
    mc_src_sha="$(sha256sum "$MEDIACAPTURER_SRC" | awk '{print $1}')"
    if [ "$mc_dst_sha" != "$mc_src_sha" ]; then
      echo "[-] /data MediaCapturer APK write mismatch" >&2
      echo "    src: $mc_src_sha" >&2
      echo "    dst: $mc_dst_sha" >&2
      exit 1
    fi
    echo "[+] non-persistent MediaCapturer patch staged in /data/june/mediacapturer_nopin.apk ($(stat -c '%s' "$MEDIACAPTURER_SRC") bytes)"
  else
    echo "[ ] persistent MediaCapturer patch staged in /system/etc/june/mediacapturer_nopin.apk"
  fi
else
  rm -f "$UDA_MNT/june/mediacapturer_nopin.apk"
fi

# Chef cook-modes patch. When it could not be persisted in both system
# partitions, stage a /data copy: june_reaper applies it on the next boot and
# removes the staged file afterwards. A persistent /system copy wins on the
# next boot; the /data copy stays as a post-factory-reset fallback.
if [ -n "$CHEF_SRC" ]; then
  if [ "${CHEF_PERSISTED:-0}" -ge 2 ]; then
    echo "[ ] Chef patch persisted in both system partitions; skipping /data staging"
  else
    chef_size="$(stat -c '%s' "$CHEF_SRC" 2>/dev/null || echo 0)"
    chef_need_kb=$(( ((chef_size + 1023) / 1024) * 2 + 65536 ))
    chef_avail_kb="$(df -Pk "$UDA_MNT" 2>/dev/null | awk 'NR==2 {print $4}')"
    if [ "${chef_avail_kb:-0}" -ge "$chef_need_kb" ]; then
      install -m 0644 "$CHEF_SRC" "$UDA_MNT/june/chef_cookmodes.apk"
      chef_dst_sha="$(sha256sum "$UDA_MNT/june/chef_cookmodes.apk" | awk '{print $1}')"
      chef_src_sha="$(sha256sum "$CHEF_SRC" | awk '{print $1}')"
      if [ "$chef_dst_sha" != "$chef_src_sha" ]; then
        echo "[-] /data Chef APK write mismatch" >&2
        echo "    src: $chef_src_sha" >&2
        echo "    dst: $chef_dst_sha" >&2
        exit 1
      fi
      echo "[+] Chef patch staged in /data/june/chef_cookmodes.apk ($chef_size bytes; applied on next boot)"
    else
      echo "[!] not enough space on /data for the Chef patch (need $(( chef_need_kb / 1024 )) MB, have $(( ${chef_avail_kb:-0} / 1024 )) MB); cook modes stay stock" >&2
    fi
  fi
fi

# Presets/recipes content mirror: june-local serves the catalog and program
# bundles from /capture/upstream-content (june_reaper links /capture to /data),
# which is what guided programs such as Proof and Slow Cook load.
if [ -d "$SRC_DIR/upstream-content" ]; then
  content_kb="$(du -sk "$SRC_DIR/upstream-content" | awk '{print $1}')"
  avail_kb="$(df -Pk "$UDA_MNT" 2>/dev/null | awk 'NR==2{print $4}')"
  if [ "${avail_kb:-0}" -gt $(( content_kb + 65536 )) ]; then
    echo "[*] installing preset/recipe content ($(( content_kb / 1024 )) MB) to /data/june/upstream-content..."
    rm -rf "$UDA_MNT/june/upstream-content"
    cp -a "$SRC_DIR/upstream-content" "$UDA_MNT/june/upstream-content"
    echo "[+] preset content installed ($(du -sh "$UDA_MNT/june/upstream-content" 2>/dev/null | cut -f1)); guided programs enabled"
  else
    echo "[!] not enough space on /data for preset content (need $(( content_kb / 1024 )) MB, have $(( ${avail_kb:-0} / 1024 )) MB); guided programs will be unavailable" >&2
  fi
else
  echo "[i] no preset content in the payload; guided programs (proof, slow cook, recipes) will be unavailable"
fi

sync
umount "$UDA_MNT"
losetup -d "$LOOP_UDA"
LOOP_UDA=""

echo "[+] deployed. SHA256 of staged files:"
if [ "$FLASH_BOOT" = "1" ]; then
  sha256sum "$BIN_SRC" "$CAMERAD_SRC" "$CERT_SRC" "$KEY_SRC" "$JCM_SRC" "$BOOTCLEAR_SRC" "$JUNE_REAPER_SRC" "$COMPANION_STATE_SRC" "$BOOT_SRC" "$LIGHT_SRC" ${REMOTES_SRC:+"$REMOTES_SRC"} ${MEDIACAPTURER_SRC:+"$MEDIACAPTURER_SRC"} ${ANALYTICS_SRC:+"$ANALYTICS_SRC"} ${CHEF_SRC:+"$CHEF_SRC"}
else
  sha256sum "$BIN_SRC" "$CAMERAD_SRC" "$CERT_SRC" "$KEY_SRC" "$JCM_SRC" "$BOOTCLEAR_SRC" "$JUNE_REAPER_SRC" "$COMPANION_STATE_SRC" "$LIGHT_SRC" ${REMOTES_SRC:+"$REMOTES_SRC"} ${MEDIACAPTURER_SRC:+"$MEDIACAPTURER_SRC"} ${ANALYTICS_SRC:+"$ANALYTICS_SRC"} ${CHEF_SRC:+"$CHEF_SRC"}
fi
if [ -f "$DNS_SRC" ]; then
  sha256sum "$DNS_SRC"
fi
if [ -n "$IDENTITY_SEED" ] && [ -f "$IDENTITY_SEED" ]; then
  sha256sum "$IDENTITY_SEED" "$IDENTITY_JSON"
fi
echo "[+] power-cycle the oven normally; the junelocal init service starts june-local at boot_completed."
