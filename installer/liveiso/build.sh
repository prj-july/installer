#!/usr/bin/env bash
set -euo pipefail

OUT="${1:-/out/june-local-live.iso}"
WORK="/build/live"
rm -rf "$WORK"
mkdir -p "$WORK" /out
cd "$WORK"

# Installer payload (everything a friend needs to run the on-oven deploy).
PAYLOAD="/opt/june-installer"
mkdir -p "config/includes.chroot$PAYLOAD"
rsync -a /build/assets/ "config/includes.chroot$PAYLOAD/"
# Release version stamped by build_live_iso.sh; shown in the boot menus.
JUNE_VERSION="$(cat /build/assets/VERSION 2>/dev/null || echo dev)"

# The source tree may live on CIFS, where chmod +x does not stick.  Ensure the
# on-oven binaries/scripts are executable inside the final live image.
chmod +x \
  "config/includes.chroot$PAYLOAD/install.sh" \
  "config/includes.chroot$PAYLOAD/backup.sh" \
  "config/includes.chroot$PAYLOAD/deploy_onoven.sh" \
  "config/includes.chroot$PAYLOAD/generate_certs.sh" \
  "config/includes.chroot$PAYLOAD/sign_leaf.sh" \
  "config/includes.chroot$PAYLOAD/tegrarcm" \
  "config/includes.chroot$PAYLOAD/june-backup" \
  "config/includes.chroot$PAYLOAD/generate_identity.py" \
  "config/includes.chroot$PAYLOAD/june-local-linux-armv7" \
  "config/includes.chroot$PAYLOAD/june-camerad-linux-armv7" \
  "config/includes.chroot$PAYLOAD/june-wireless-update"
mkdir -p config/includes.chroot/usr/local/bin
ln -sf "$PAYLOAD/june-wireless-update" config/includes.chroot/usr/local/bin/june-wireless-update

# Graphical installer: Python backend + web UI (see gui/).  The dev/ mock
# tooling stays out of the image.
rsync -a --exclude dev/ /build/gui/ "config/includes.chroot$PAYLOAD/gui/"
mkdir -p config/includes.chroot/etc/systemd/system config/includes.chroot/usr/local/bin
mv "config/includes.chroot$PAYLOAD/gui/june-gui.service" config/includes.chroot/etc/systemd/system/
mv "config/includes.chroot$PAYLOAD/gui/june-kiosk" config/includes.chroot/usr/local/bin/
chmod +x config/includes.chroot/usr/local/bin/june-kiosk

# cage does not expose the wlr virtual-pointer protocol, so the kiosk wakes
# the cursor with a real uinput mouse instead (see vendor/cursor-nudge).
mkdir -p config/includes.chroot/usr/bin
gcc -O2 -Wall -o config/includes.chroot/usr/bin/june-cursor-nudge \
    /build/vendor/cursor-nudge/cursor-nudge.c

# Extra runtime packages for the live system (menu UI + deploy dependencies,
# then the GUI: cage kiosk compositor, cog/WPE WebKit, Mesa for GPU and
# software rendering, logind seat access, the backend and QR codes).
mkdir -p config/package-lists
cat > config/package-lists/june.list.chroot <<'EOF'
whiptail
dialog
sudo
openssl
e2fsprogs
util-linux
usbutils
libcrypto++8
libusb-1.0-0
libstdc++6
cage
cog
xwayland
adwaita-icon-theme
xcursor-themes
seatd
libgl1-mesa-dri
libegl-mesa0
dbus
libpam-systemd
python3
qrencode
iproute2
fonts-dejavu-core
# GPU + Wi-Fi firmware: without these, laptops whose display driver needs
# firmware (AMD amdgpu, Intel i915, NVIDIA nouveau) never create /dev/dri,
# so the cage/Wayland kiosk cannot start ("Found 0 GPUs").
firmware-amd-graphics
firmware-misc-nonfree
firmware-iwlwifi
firmware-realtek
EOF

# Wired DHCP so a phone on the same network can reach the installer.
mkdir -p config/includes.chroot/etc/systemd/network
cat > config/includes.chroot/etc/systemd/network/20-june-wired.network <<'EOF'
[Match]
Name=en* eth*

[Network]
DHCP=yes
EOF

# Enable the GUI backend and networkd; mask ifupdown (if present) so a NIC
# never gets two DHCP clients.
mkdir -p config/hooks/normal
cat > config/hooks/normal/9100-june-services.hook.chroot <<'EOF'
#!/bin/sh
set -e
systemctl enable june-gui.service systemd-networkd.service seatd.service
for unit in networking.service ifup@.service; do
  if [ -e "/lib/systemd/system/$unit" ]; then
    systemctl mask "$unit"
  fi
done
EOF
chmod +x config/hooks/normal/9100-june-services.hook.chroot

# The live user needs access to the seatd socket before login: cage gets its
# DRM/VT session through seatd (this image has no usable logind seat).
mkdir -p config/includes.chroot/usr/lib/live/config
cat > config/includes.chroot/usr/lib/live/config/0040-june-seat <<'EOF'
#!/bin/sh

Init ()
{
	:
}

Config ()
{
	if getent passwd "${LIVE_USERNAME}" >/dev/null 2>&1
	then
		# seatd provides the DRM/VT session; video/render are a belt-and-braces
		# fallback for AMD/Intel render nodes when libseat cannot pass them.
		for grp in seat video render; do
			getent group "$grp" >/dev/null 2>&1 && \
				usermod -aG "$grp" "${LIVE_USERNAME}" 2>/dev/null || true
		done
	fi
}
EOF
chmod +x config/includes.chroot/usr/lib/live/config/0040-june-seat

# Menu + helper available on the live system.
mkdir -p config/includes.chroot/usr/local/bin
cat > config/includes.chroot/usr/local/bin/june-menu <<'EOF'
#!/bin/bash
set -u

PAYLOAD=/opt/june-installer
INSTALL="$PAYLOAD/install.sh"
BACKUP="$PAYLOAD/backup.sh"
GUIDE_URL="https://project-july.org/setup"
BUILD_ID="$(cat "$PAYLOAD/BUILD_ID" 2>/dev/null || echo unknown)"
VERSION="$(cat "$PAYLOAD/VERSION" 2>/dev/null || echo unknown)"

on_interrupt() {
  stty sane 2>/dev/null || true
  printf '\033[0m\033[?25h' 2>/dev/null || true
  clear 2>/dev/null || true
  exit 130
}
trap on_interrupt INT TERM HUP

# Keep kernel messages (e.g. Bluetooth -110 timeouts) off the menu.  loglevel=0
# on the cmdline does not always survive to here, so pin it again.
sudo -n dmesg -n 1 2>/dev/null || true

banner() {
  echo "=============================================="
  echo " June Local installer"
  echo " $VERSION (build $BUILD_ID)"
  echo "=============================================="
}

show_status() {
  echo
  echo "[*] RCM/APX device (expect 0955:7740):"
  if command -v lsusb >/dev/null 2>&1; then
    lsusb | grep -i "0955:7740" || echo "    not present yet"
  else
    echo "    lsusb not installed"
  fi
  echo
  echo "[*] Block devices:"
  lsblk -o NAME,SIZE,TYPE,TRAN,MODEL 2>/dev/null || true
  echo
  echo "[*] USB connection guide: $GUIDE_URL"
  echo
  read -rp "Press Enter to return to the menu: "
}

device_summary() {
  echo "RCM/APX (expect 0955:7740):"
  if command -v lsusb >/dev/null 2>&1; then
    lsusb | grep -i "0955:7740" || echo "  not present"
  else
    echo "  lsusb not installed"
  fi
  echo
  echo "Block devices:"
  lsblk -o NAME,SIZE,TYPE,TRAN,MODEL 2>/dev/null || true
}

show_devices_dialog() {
  local title="$1"
  whiptail --title "$title" --msgbox "$(device_summary)" 20 78
}

copy_logs_to() {
  local mnt="$1"
  [ -n "$mnt" ] || return 1
  [ -d "$mnt" ] || return 1
  sudo -n mkdir -p "$mnt/june-logs" "$mnt/june-certs" 2>/dev/null || return 1
  sudo -n cp "$INSTALL_LOG" "$mnt/june-logs/" 2>/dev/null || return 1
  if [ -d /opt/june-installer/certs ]; then
    sudo -n cp /opt/june-installer/certs/ca.crt "$mnt/june-certs/ca.crt" 2>/dev/null || true
    sudo -n cp /opt/june-installer/certs/leaf.crt "$mnt/june-certs/leaf.crt" 2>/dev/null || true
    sudo -n cp /opt/june-installer/certs/fullchain.crt "$mnt/june-certs/fullchain.crt" 2>/dev/null || true
  fi
  sudo -n sync 2>/dev/null || true
  printf '%s\n' "$mnt"
  return 0
}

INSTALL_LOG=""
BACKUP_DEST=""
BACKUP_FOLDER=""
PERSIST_MSG=""
DEST_LOG=/tmp/june-dest.log
IMAGE_BYTES=7818182656

bytes_to_gib() {
  awk -v b="${1:-0}" 'BEGIN { printf "%.1f", b/1073741824 }'
}

# Newest "[*] ..." style line from the worker log (the current stage).
log_stage_line() {
  local log="$1" line
  line="$(tr '\r' '\n' < "$log" 2>/dev/null | grep -E '^\[[*+ -]\] ' | tail -n 1 | cut -c1-110 || true)"
  printf '%s' "$line"
}

# Newest dd progress record in the log as "<bytes> <MB/s>".  dd pads shorter
# records with trailing spaces and switches between kB/s, MB/s and GB/s, so
# match loosely and normalise the rate (0 when unknown).
log_dd_record() {
  tr '\r' '\n' < "$1" 2>/dev/null \
    | grep -E '^[0-9]+ bytes .*copied, ' | tail -n 1 \
    | awk '{
        r = 0; u = ""
        for (i = NF; i > 1; i--) if ($i ~ /\/s$/) { r = $(i-1); u = $i; break }
        if (u == "kB/s") r /= 1000
        else if (u == "GB/s") r *= 1000
        else if (u == "B/s" || u == "bytes/s") r /= 1000000
        else if (u != "MB/s") r = 0
        printf "%s %.1f\n", $1, r
      }'
}

# Percentage of the oven image copied so far (0 when no dd record yet).
log_progress_pct() {
  local rec bytes
  rec="$(log_dd_record "$1")"
  bytes="${rec%% *}"
  case "$bytes" in ''|*[!0-9]*) echo 0; return ;; esac
  [ "$bytes" -le "$IMAGE_BYTES" ] || bytes="$IMAGE_BYTES"
  echo $(( bytes * 100 / IMAGE_BYTES ))
}

# One clean progress line built from the newest dd progress record in the log.
log_progress_line() {
  local log="$1" rec bytes rate pct copied remain eta_s eta
  rec="$(log_dd_record "$log")"
  [ -n "$rec" ] || return 1
  bytes="${rec%% *}"
  rate="${rec#* }"
  case "$bytes" in ''|*[!0-9]*) return 1 ;; esac
  [ "$bytes" -le "$IMAGE_BYTES" ] || bytes="$IMAGE_BYTES"
  pct=$(( bytes * 100 / IMAGE_BYTES ))
  copied="$(bytes_to_gib "$bytes")"
  remain=$(( IMAGE_BYTES - bytes ))
  if awk -v r="${rate:-0}" 'BEGIN { exit !(r > 0.1) }'; then
    eta_s="$(awk -v rem="$remain" -v r="$rate" 'BEGIN { printf "%d", (rem/1000000)/r }')"
    if [ "$eta_s" -ge 90 ]; then
      eta="$(( eta_s / 60 )) min left"
    else
      eta="$eta_s s left"
    fi
    printf 'Copied %s / 7.3 GiB (%s%%) at %s MB/s - %s' "$copied" "$pct" "$rate" "$eta"
  else
    printf 'Copied %s / 7.3 GiB (%s%%) - estimating speed...' "$copied" "$pct"
  fi
}

progress_text() {
  local log="$1" stage prog
  stage="$(log_stage_line "$log")"
  prog="$(log_progress_line "$log" || true)"
  [ -n "$stage" ] || stage="Starting..."
  printf '%s\n%s' "$stage" "${prog:-Starting...}"
}

oven_present() {
  local b dev sz
  if command -v lsusb >/dev/null 2>&1 && lsusb 2>/dev/null | grep -qi '0955:7740'; then
    return 0
  fi
  for b in /sys/block/sd*; do
    [ -e "$b" ] || continue
    dev="/dev/$(basename "$b")"
    [ -b "$dev" ] || continue
    sz="$(blockdev --getsize64 "$dev" 2>/dev/null || echo 0)"
    [ "$sz" = "$IMAGE_BYTES" ] && return 0
  done
  return 1
}

# Never leave the user staring at a silent screen when the oven is not
# connected yet: offer an explicit retry loop with guidance.
await_oven() {
  local start now elapsed choice
  oven_present && return 0
  start="$(date +%s)"
  while :; do
    now="$(date +%s)"
    elapsed=$(( now - start ))
    choice="$(whiptail --title "Waiting for the oven" \
      --menu "The oven is not detected yet (waiting ${elapsed}s).\n\n1. Check the USB cable between the oven and this computer.\n2. Hold SW2 (FORCE_RECOVERY) and power the oven on, keep holding ~10 seconds.\n3. Then choose Try again.\n\nThe installer will keep trying for you; it never needs a keypress to continue." \
      20 78 3 \
      "retry" "Check again now" \
      "continue" "Continue anyway (advanced)" \
      "back" "Back to the main menu" 3>&1 1>&2 2>&3)" || return 1
    case "$choice" in
      retry) oven_present && return 0 ;;
      continue) return 0 ;;
      *) return 1 ;;
    esac
  done
}

# Run the on-oven installer while showing everything it does, and keep a
# timestamped log so failures are still readable after whiptail redraws.
run_install() {
  local mode="$1"; shift
  local pid rc
  INSTALL_LOG="$(mktemp /tmp/june-install.XXXXXX.log)"
  if command -v whiptail >/dev/null 2>&1; then
    (sudo -n bash "$INSTALL" "$mode" "$@" >"$INSTALL_LOG" 2>&1) &
    pid=$!
    while kill -0 "$pid" 2>/dev/null; do
      whiptail --title "Installing June Local" \
        --infobox "$(progress_text "$INSTALL_LOG")

Keep the oven in RCM mode and do not unplug anything.
Waiting for the oven (APX 0955:7740) can take up to 2 minutes.
Once patching starts the oven fans spin up faster and keep spinning while it works; this may take up
to about 2 minutes, then the installer reports success or failure." \
        17 78
      sleep 1
    done
    wait "$pid"; rc=$?
    clear
  else
    sudo -n bash "$INSTALL" "$mode" "$@" 2>&1 | tee "$INSTALL_LOG"
    rc=${PIPESTATUS[0]}
  fi
  return "$rc"
}

run_backup() {
  local mode="$1"; shift
  local pid rc msg feed
  INSTALL_LOG="$(mktemp /tmp/june-backup.XXXXXX.log)"
  if command -v whiptail >/dev/null 2>&1; then
    # Background jobs get /dev/null as stdin; restore's confirmation prompt
    # needs the answer the menu already collected.
    feed=""
    [ "$mode" = "restore" ] && feed="RESTORE"
    (printf '%s\n' "$feed" | sudo -n bash "$BACKUP" "$mode" "$@" >"$INSTALL_LOG" 2>&1) &
    pid=$!
    # whiptail --gauge protocol: "XXX", percent, prompt, "XXX".  whiptail only
    # reads one prompt line, so send line breaks as a literal \n it expands.
    # The text area keeps the height of the initial prompt, hence the padding.
    while kill -0 "$pid" 2>/dev/null; do
      msg="$(progress_text "$INSTALL_LOG")

The oven image is 7.3 GiB (~7.8 GB) and is written as one file.
Typical: ~26 min at 5 MB/s, ~13 min at 10 MB/s, ~9 min at 15 MB/s.
Do not unplug the oven or the USB drive."
      printf 'XXX\n%s\n%s\nXXX\n' "$(log_progress_pct "$INSTALL_LOG")" "${msg//$'\n'/\\n}"
      sleep 1
    done | whiptail --title "Backup in progress" \
      --gauge "Starting...\n\n\n\n\n\n\n" 14 78 0
    wait "$pid"; rc=$?
    clear
  else
    sudo -n bash "$BACKUP" "$mode" "$@" 2>&1 | tee "$INSTALL_LOG"
    rc=${PIPESTATUS[0]}
  fi
  return "$rc"
}

choose_destination() {
  local purpose="${1:-backup}" list line tag label avail origin desc args=() choice folder_filename folder gb ventoy_mnt reasons

  while :; do
    args=()
    : > "$DEST_LOG" 2>/dev/null || true
    list="$(sudo -n bash "$BACKUP" list 2>>"$DEST_LOG" || true)"
    ventoy_mnt="$(sudo -n bash "$BACKUP" ventoy-dir 2>>"$DEST_LOG" || true)"
    if [ -z "$list" ]; then
      reasons="$(tail -n 6 "$DEST_LOG" 2>/dev/null | cut -c1-96 || true)"
      if [ -n "$ventoy_mnt" ]; then
        choice="$(whiptail --title "Backup destination" \
          --menu "This live system runs from RAM. If the boot USB does not have enough storage, you can safely remove it now and plug in another USB drive, then choose Refresh.\n\n${reasons:+Details:\n$reasons\n\n}Select where to save the backup image:" \
          22 78 3 \
          "$ventoy_mnt" "Boot USB stick - current boot medium" \
          "refresh" "Rescan connected drives" \
          "back" "Back to main menu" 3>&1 1>&2 2>&3)" || return 1
        [ "$choice" = "back" ] && return 1
        [ "$choice" = "refresh" ] && continue
        break
      fi
      choice="$(whiptail --title "Backup destination" \
        --menu "This live system runs from RAM. If the boot USB does not have enough storage, you can safely remove it now and plug in another USB drive, then choose Refresh.\n\nNo safe writable destination was found. Plug in an exFAT/NTFS/ext4 USB drive with at least 8 GB free.\n\n${reasons:+Details:\n$reasons}" \
        18 78 2 \
        "refresh" "Rescan connected drives" \
        "back" "Back to main menu" 3>&1 1>&2 2>&3)" || return 1
      [ "$choice" = "back" ] && return 1
      continue
    fi
    while IFS=$'\t' read -r tag label avail origin; do
      [ -n "$tag" ] || continue
      gb=$((avail / 1000000000))
      desc="${label:-$tag} (${gb} GB free)"
      [ "$origin" = "internal" ] && desc="${desc} - internal"
      args+=("$tag" "$desc")
    done <<< "$list"
    case "$list" in
      *"(current boot USB)"*) ;;
      *)
        if [ -n "$ventoy_mnt" ]; then
          args+=("$ventoy_mnt" "Boot USB stick - current boot medium")
        fi
        ;;
    esac
    args+=("refresh" "Rescan connected drives")
    choice="$(whiptail --title "Choose backup destination" \
      --menu "This live system runs from RAM. If the boot USB does not have enough storage, you can safely remove it now and plug in another USB drive, then choose Refresh.\n\nSelect where to save the backup image:" \
      22 78 12 "${args[@]}" 3>&1 1>&2 2>&3)" || return 1
    [ "$choice" = "refresh" ] && continue
    break
  done

  if [ "$purpose" = "backup" ]; then
    folder_filename="$(dialog --stdout --no-cancel --visit-items --form "Optional backup location and filename" 0 0 3 \
      "Folder (blank = root):" 1 1 "" 1 40 70 0 \
      "Filename:" 2 1 "$(date +%y%m%d-%H%M).img" 2 40 70 0)" || continue
    clear
    BACKUP_FOLDER="$(printf '%s\n' "$folder_filename" | sed -n '1p')"
    BACKUP_FILENAME="$(printf '%s\n' "$folder_filename" | sed -n '2p')"
    BACKUP_FILENAME="${BACKUP_FILENAME:-$(date +%y%m%d-%H%M).img}"
  else
    folder="$(whiptail --title "Restore folder" \
      --inputbox "Optional subfolder on the selected destination (blank = root):" \
      10 78 "" --ok-button "Next" --cancel-button "Back" 3>&1 1>&2 2>&3)" || continue
    BACKUP_FOLDER="$folder"
  fi
  BACKUP_DEST="$choice"
}

generate_flow() {
  local result ip host persist_flag persist_choice
  form_text="Custom CA mode creates a private Certificate Authority on this live USB.

It signs a unique oven certificate for your LAN IP and hostname, installs that CA on the oven, and leaves ca.crt for you to install on the phone.

Oven LAN IP and hostname:"
  result="$(dialog --stdout --no-cancel --visit-items --form "$form_text" 0 0 3 \
    "Oven LAN IP (e.g. 192.168.1.100):" 1 1 "192.168.1.100" 1 35 36 0 \
    "Hostname:" 2 1 "june.local" 2 35 36 0)" || return 1
  clear
  ip="$(printf '%s\n' "$result" | sed -n '1p')"
  host="$(printf '%s\n' "$result" | sed -n '2p')"
  [ -n "$ip" ] || {
    whiptail --title "Per-home CA + leaf" --msgbox "Oven LAN IP cannot be empty." 8 78
    return 1
  }
  host="${host:-june.local}"

  persist_flag=""
  if whiptail --title "Factory-reset persistence" --defaultno \
    --yesno "Keep June Local after a factory reset?\n\nNo (default): the June Local patches (cook modes, camera, light, messaging) and the per-home CA are staged in /data only; a factory reset removes them.\nYes: they are staged in /system and re-applied after a factory reset." \
    16 78 --yes-button "Persist" --no-button "Do not persist"; then
    persist_flag="persist"
  fi

  whiptail --title "Per-home CA + leaf" \
    --yesno "Connect the oven with the USB cable and put it in RCM mode:\n\n1. Hold SW2 (FORCE_RECOVERY).\n2. Power the oven on and keep holding SW2 for about 10 seconds.\n\nOnce patching starts the oven fans spin up faster and keep spinning while it works; this may take up to about 2 minutes.\n\nConnection guide: $GUIDE_URL\n\nPress Continue when ready, or Back to return to the menu." \
    18 78 --yes-button "Continue" --no-button "Back" || return 1
  await_oven || return 1

  if run_install generate "$ip" "$host" "$persist_flag"; then
    PATCH_STAGED=1
    persist_log
    show_success "Then install ca.crt on the phone, install the companion app, and set the oven endpoint.${persist_flag:+ The June Local patches are staged in /system and persist across factory resets.}" "http://${host}/local"
  else
    persist_log
    show_failure "Install"
  fi
}

persist_log() {
  [ -n "${INSTALL_LOG:-}" ] || return 0
  local target cand saved=""
  PERSIST_MSG=""

  target="$(sudo -n bash "$BACKUP" ventoy-dir 2>>"$DEST_LOG" || true)"
  if [ -n "$target" ]; then
    saved="$(copy_logs_to "$target" || true)"
  fi

  if [ -z "$saved" ] && [ -n "${BACKUP_DEST:-}" ]; then
    case "$BACKUP_DEST" in
      /*) cand="$BACKUP_DEST" ;;
      *) cand="$(findmnt -rno TARGET --source "$BACKUP_DEST" 2>/dev/null | head -n 1 || true)" ;;
    esac
    if [ -n "$cand" ] && [ "$cand" != "$target" ]; then
      saved="$(copy_logs_to "$cand" || true)"
    fi
  fi

  if [ -z "$saved" ]; then
    for cand in /run/live/medium /lib/live/mount/medium; do
      [ -d "$cand" ] || continue
      saved="$(copy_logs_to "$cand" || true)"
      [ -n "$saved" ] && break
    done
  fi

  if [ -n "$saved" ]; then
    PERSIST_MSG="Installer log saved to $saved/june-logs/$(basename "$INSTALL_LOG")."
    [ -d /opt/june-installer/certs ] && PERSIST_MSG="$PERSIST_MSG
CA certificate files (if custom install) saved to $saved/june-certs/."
  else
    PERSIST_MSG="No writable USB drive was found for the log.
Full log stays at: $INSTALL_LOG"
  fi
  return 0
}

show_success() {
  local extra="$1" oven_url="${2:-}" patch_line=""
  if [ "${PATCH_STAGED:-0}" = "1" ]; then
    patch_line="The June Local patches are applied on the oven's next power-on.\n\n"
  fi
  whiptail --title "June Local installer" \
    --msgbox "Success! June Local is installed on the oven.\n\n${patch_line}TEST NOW (recommended, before closing up):\n1. Unplug the oven power and the USB cable from this computer.\n2. Wait ~10 seconds, then plug the power back in.\n3. Let the oven boot normally.\n4. Check June Local (next screen) and pair from the June app / Home Assistant.\n\nOR CLOSE UP FIRST:\n1. Unplug power and USB, then close/reassemble the oven.\n2. Power it on normally - it should just work.\n3. Pair when convenient.\n\n${extra}\n\n${PERSIST_MSG}" \
    24 78
  whiptail --title "Check June Local is running" \
    --msgbox "From any device on the same network, open:\n\n    ${oven_url:-http://<oven-ip>/local}\n\nYou should see the June Local status page. A machine-readable check is:\n    ${oven_url:-http://<oven-ip>/local}/status\n\nThen pair/control from the June app or Home Assistant. Live temperature and cook controls with no cloud errors mean the oven is running June Local." \
    18 78
  if command -v qrencode >/dev/null 2>&1; then
    clear
    echo "=============================================================="
    echo "  June Local is installed"
    echo "=============================================================="
    if [ -n "$oven_url" ]; then
      echo
      echo "Status page: $oven_url"
      qrencode -t ANSIUTF8 "$oven_url" 2>/dev/null || true
    fi
    echo
    echo "Setup guide: $GUIDE_URL"
    qrencode -t ANSIUTF8 "$GUIDE_URL" 2>/dev/null || true
    echo
    read -r -p "Press Enter to return to the menu..." _ || true
  fi
}

show_failure() {
  local action="${1:-Install}" choice reason=""
  # Surface the worker's own "[-] ..." explanation instead of only the log path.
  if [ -n "${INSTALL_LOG:-}" ]; then
    reason="$(tr '\r' '\n' < "$INSTALL_LOG" 2>/dev/null | grep -E '^\[-\] ' | tail -n 3 | cut -c5-200 || true)"
  fi
  choice="$(whiptail --title "June Local installer" \
    --menu "${action} did not finish.\n\n${reason:+$reason\n\n}${PERSIST_MSG}\n\nWhat would you like to do next?" \
    24 78 3 \
    "log" "Show the full log" \
    "back" "Back to the menu" 3>&1 1>&2 2>&3)" || return 0
  if [ "$choice" = "log" ]; then
    whiptail --title "$action log" --textbox "$INSTALL_LOG" 25 78
  fi
}

show_backup_done() {
  whiptail --title "June Local installer" \
    --msgbox "Backup complete.\n\nSafely unplug the backup USB drive, then unplug the oven power and USB cable. Reassemble the oven before powering it on.\n\n${PERSIST_MSG}" \
    14 78
}

show_restore_done() {
  whiptail --title "June Local installer" \
    --msgbox "Restore complete.\n\nUnplug the oven power and USB cable, reassemble the oven, then power it on normally." \
    10 78
}

if command -v whiptail >/dev/null 2>&1; then
  while true; do
    CHOICE="$(whiptail --title "June Local installer" \
      --menu "Version: $VERSION (build $BUILD_ID)\n\nConnect the oven via USB. Guide: $GUIDE_URL\n\nChoose an action:" \
      20 78 8 \
      "install" "Install June Local (recommended)" \
      "custom" "Install with per-home CA + leaf (advanced)" \
      "backup" "Backup oven to USB (needs 8 GB free)" \
      "restore" "Restore oven from USB backup" \
      "status" "Check USB / disk status" \
      "shell" "Exit to a shell" \
      "reboot" "Exit installer and reboot" 3>&1 1>&2 2>&3)" || break

    case "$CHOICE" in
      install)
        oven_name="$(whiptail --title "June Local installer" \
          --inputbox "Name for this June oven, used for its mDNS address <name>.local:\n\nThe basic install adds the project CA to the oven's trust store; the certificate covers june.local, june2.local and june3.local. The JuneDns helper advertises <name>.local; if multicast (mDNS) is blocked, add a local DNS entry or use Connect by IP instead.\n\nOptional for other devices: point api.junelife.com and messaging.junelife.com at the oven's IP in your DNS (or hosts file). The prepackaged certificate matches those names, so even stock June clients verify there." \
          17 78 "june" --ok-button "Next" --cancel-button "Back" 3>&1 1>&2 2>&3)" || continue
        oven_name="${oven_name:-june}"
        persist_flag=""
        if whiptail --title "Factory-reset persistence" --defaultno \
          --yesno "Keep June Local after a factory reset?\n\nNo (default, recommended): the June Local patches (cook modes, camera, light, messaging) are staged in /data only; a factory reset removes them.\nYes: they are staged in /system and re-applied after a factory reset." \
          16 78 --yes-button "Persist" --no-button "Do not persist"; then
          persist_flag="persist"
        fi
        whiptail --title "June Local installer" \
          --yesno "Put the oven in RCM mode:\n\n1. Connect the oven with the USB cable.\n2. Hold SW2 (FORCE_RECOVERY).\n3. Power the oven on and keep holding SW2 for about 10 seconds.\n\nOnce patching starts the oven fans spin up faster and keep spinning while it works; this may take up to about 2 minutes.\n\nConnection guide: $GUIDE_URL\n\nPress Continue when the oven is in RCM mode, or Back to return to the menu." \
          18 78 --yes-button "Continue" --no-button "Back" || continue
        await_oven || continue
        install_ok=0
        PATCH_STAGED=0
        [ "$persist_flag" = "persist" ] && PATCH_STAGED=1
        run_install prepackaged "$oven_name" "$persist_flag" || install_ok=1
        if [ "$install_ok" -eq 0 ]; then
          persist_log
          show_success "Then install the companion app and set the oven endpoint using the steps printed below.${persist_flag:+ The June Local patches are staged in /system and persist across factory resets; the oven applies them on the next boot.}" "http://${oven_name}.local/local"
        else
          persist_log
          show_failure
        fi
        ;;
      custom)
        generate_flow
        ;;
      backup)
        choose_destination backup || continue
        whiptail --title "Backup oven to USB" \
          --yesno "This makes a full, byte-for-byte backup of the June eMMC to:\n\n$BACKUP_DEST${BACKUP_FOLDER:+/$BACKUP_FOLDER}\n\nA full backup usually takes 20-30 minutes depending on the USB drive speed.\n\nPut the oven in RCM mode, then press Continue." \
          18 78 --yes-button "Continue" --no-button "Back" || continue
        await_oven || continue
        if run_backup backup "$BACKUP_DEST" "$BACKUP_FOLDER" "$BACKUP_FILENAME"; then
          persist_log
          show_backup_done
        else
          persist_log
          show_failure "Backup"
        fi
        ;;
      restore)
        choose_destination restore || continue
        whiptail --title "Restore oven from USB" \
          --yesno "This writes a previously created backup image back to the June eMMC from:\n\n$BACKUP_DEST${BACKUP_FOLDER:+/$BACKUP_FOLDER}\n\nThis DESTROYS the current contents of the oven. Put the oven in RCM mode." \
          16 78 --yes-button "Continue" --no-button "Back" || continue
        # The worker runs in the background with no terminal, so collect its
        # RESTORE confirmation here and hand it over on stdin (run_backup).
        confirm="$(whiptail --title "Confirm restore" \
          --inputbox "Type RESTORE to erase the oven and restore the backup:" \
          10 78 "" --ok-button "Restore" --cancel-button "Back" 3>&1 1>&2 2>&3)" || continue
        if [ "$confirm" != "RESTORE" ]; then
          whiptail --title "Restore oven from USB" --msgbox "Restore cancelled: the confirmation did not match RESTORE." 8 78
          continue
        fi
        await_oven || continue
        if run_backup restore "$BACKUP_DEST" "$BACKUP_FOLDER"; then
          persist_log
          show_restore_done
        else
          persist_log
          show_failure "Restore"
        fi
        ;;
      status)
        show_status
        ;;
      shell)
        stty sane 2>/dev/null || true
        printf '\033[0m\033[?25h'
        banner
        echo "Dropping to a shell. Run 'june-menu' to return here."
        break
        ;;
      reboot)
        whiptail --title "Exit installer" \
          --yesno "Exit the installer and reboot this computer?\n\nRemove the USB stick when prompted so the computer boots back into its normal operating system." \
          10 78 --yes-button "Reboot" --no-button "Back" || continue
        sudo -n systemctl reboot
        ;;
      *)
        break
        ;;
    esac
  done
else
  banner
  echo
  echo "1) Connect by name (june.local)"
  echo "2) Connect by IP (recommended; per-home CA)"
  echo "3) Backup oven to USB (needs 8 GB free)"
  echo "4) Restore oven from USB backup"
  echo "5) Check USB / disk status"
  echo "6) Exit to a shell"
  echo "7) Exit installer and reboot"
  echo
  read -rp "Choice [2]: " choice
  case "${choice:-2}" in
    1)
      read -rp "Name for this June oven [june]: " oven_name
      oven_name="${oven_name:-june}"
      echo "Note: this basic install adds the project CA to the oven's trust store. The"
      echo "certificate covers june.local, june2.local and june3.local, and the JuneDns"
      echo "helper advertises <name>.local. If multicast (mDNS) is blocked, add a local"
      echo "DNS entry or use Connect by IP (option 2)."
      echo "Optional (advanced): the stock June app can use api.junelife.com and"
      echo "messaging.junelife.com with a router DNS entry pointing at the oven."
      echo "Connect the oven, hold SW2, and power it on."
      echo "Once patching starts the oven fans spin up faster and keep spinning while it works;"
      echo "this may take up to about 2 minutes."
      echo "Connection guide: $GUIDE_URL"
      read -rp "Press Enter when the oven is in RCM mode: "
      if run_install prepackaged "$oven_name"; then
        echo "Success. Unplug the oven power and the USB cable."
        echo "TEST NOW: power it back on, then open http://<oven-ip>/local to confirm"
        echo "June Local. Install the CA from http://<oven-ip>/local/ca.crt on the phone,"
        echo "then pair from the June app / Home Assistant."
        echo "OR: close/reassemble the oven first - it is ready after the next normal power-on."
      else
        echo "Install did not finish. Log: $INSTALL_LOG" >&2
        exit 1
      fi
      ;;
    2)
      echo "Connect by IP: generates a private CA and a certificate for your LAN IP."
      echo "Use the IP on the phone; no hostname or DNS is needed."
      read -rp "Oven LAN IP: " oven_ip
      read -rp "Hostname (optional; for a name-based URL too): " oven_host
      echo "Connect the oven, hold SW2, and power it on."
      echo "Once patching starts the oven fans spin up faster and keep spinning while it works;"
      echo "this may take up to about 2 minutes."
      echo "Connection guide: $GUIDE_URL"
      read -rp "Press Enter when the oven is in RCM mode: "
      if run_install generate "${oven_ip:?}" "${oven_host:-june.local}"; then
        echo "Success. Unplug the oven power and the USB cable."
        echo "TEST NOW: power it back on, then open http://<oven-ip>/local to confirm"
        echo "June Local. Install the CA from http://<oven-ip>/local/ca.crt on the phone,"
        echo "then pair from the June app / Home Assistant."
        echo "OR: close/reassemble the oven first - it is ready after the next normal power-on."
      else
        echo "Install did not finish. Log: $INSTALL_LOG" >&2
        exit 1
      fi
      ;;
    3)
      echo "Connect the oven, hold SW2, and power it on."
      echo "Use a spare USB drive, or the boot USB if it has a writable data partition (JUNE or Ventoy)."
      echo "Connection guide: $GUIDE_URL"
      read -rp "Press Enter when the oven is in RCM mode: "
      if run_backup backup; then
        echo "Backup complete. Safely unplug the backup USB drive, then power off the oven."
      else
        echo "Backup did not finish. Log: $INSTALL_LOG" >&2
        exit 1
      fi
      ;;
    4)
      echo "This will overwrite the oven eMMC with the backup image on the USB drive."
      echo "Connect the oven, hold SW2, and power it on."
      read -rp "Press Enter when the oven is in RCM mode: "
      if run_backup restore; then
        echo "Restore complete. Unplug the oven power and USB cable, reassemble, then power on."
      else
        echo "Restore did not finish. Log: $INSTALL_LOG" >&2
        exit 1
      fi
      ;;
    5)
      show_status
      ;;
    6)
      echo "Run 'june-menu' to open the installer again."
      ;;
    7)
      echo "Rebooting..."
      sudo -n systemctl reboot
      ;;
    *)
      echo "Run 'june-menu' to open the installer again."
      ;;
  esac
fi
EOF
chmod +x config/includes.chroot/usr/local/bin/june-menu

# Auto-login on the first virtual console.
mkdir -p config/includes.chroot/etc/systemd/system/getty@.service.d
cat > config/includes.chroot/etc/systemd/system/getty@.service.d/autologin.conf <<'EOF'
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin user --noclear - $TERM
EOF

# Make the live user able to run the installer without a password prompt.
mkdir -p config/includes.chroot/etc/sudoers.d
cat > config/includes.chroot/etc/sudoers.d/june-live <<'EOF'
user ALL=(ALL) NOPASSWD: ALL
EOF
chmod 0440 config/includes.chroot/etc/sudoers.d/june-live

# Start the installer automatically after login, but only on a real
# interactive console so the user can still drop to a shell if needed.
# tty1 gets the graphical installer unless the "text mode" boot entry passed
# june.ui=text; june-kiosk itself falls back to the text menu.
mkdir -p config/includes.chroot/etc/profile.d
cat > config/includes.chroot/etc/profile.d/zz-june-menu.sh <<'EOF'
if [ "${BASH_VERSION:-}" ] && [ "$(id -u)" -ne 0 ]; then
  case "$(tty 2>/dev/null)" in
    /dev/tty1)
      case " $(cat /proc/cmdline 2>/dev/null) " in
        *" june.ui=text "*) [ -x /usr/local/bin/june-menu ] && /usr/local/bin/june-menu ;;
        # cage refuses to run as root and needs a seat; seatd (enabled here,
        # with the live user added to its group by the live-config component
        # 0040-june-seat) provides the DRM/VT session for the live user.
        *) [ -x /usr/local/bin/june-kiosk ] && /usr/local/bin/june-kiosk ;;
      esac
      ;;
    /dev/ttyS0)
      if [ -x /usr/local/bin/june-menu ]; then
        /usr/local/bin/june-menu
      fi
      ;;
  esac
fi
EOF

# Minimal live-build config: Debian bookworm, amd64, hybrid ISO, no desktop.
lb config \
  --distribution bookworm \
  --architectures amd64 \
  --binary-images iso-hybrid \
  --bootappend-live "boot=live components toram quiet splash loglevel=0" \
  --debian-installer false \
  --archive-areas "main contrib non-free-firmware" \
  --mirror-bootstrap http://deb.debian.org/debian \
  --mirror-chroot http://deb.debian.org/debian

# Two boot entries: the graphical installer (default) and the text installer.
# Boot the default automatically after 5 seconds instead of waiting for a key
# press.  ISOLINUX uses tenths of a second, so 50 = 5 seconds.  live-build
# fills in the @...@ kernel/initrd/append placeholders (binary_grub_cfg for
# GRUB/UEFI, live.cfg.in for ISOLINUX/BIOS).  These two heredocs are unquoted
# only to fill in $JUNE_VERSION; keep them free of other $ and backticks.
mkdir -p config/bootloaders/grub-pc config/bootloaders/isolinux
cat > config/bootloaders/grub-pc/grub.cfg <<EOF
source /boot/grub/config.cfg

menuentry "Install June Local ${JUNE_VERSION} - graphical (default)" --hotkey=g {
	linux	@KERNEL_LIVE@ @APPEND_LIVE@ june.ui=gui
	initrd	@INITRD_LIVE@
}

menuentry "Install June Local ${JUNE_VERSION} - text mode (basic UI)" --hotkey=t {
	linux	@KERNEL_LIVE@ @APPEND_LIVE@ june.ui=text
	initrd	@INITRD_LIVE@
}
EOF

cat > config/bootloaders/isolinux/live.cfg.in <<EOF
label june-gui
	menu label ^Install June Local ${JUNE_VERSION} - graphical (default)
	menu default
	linux @LINUX@
	initrd @INITRD@
	append @APPEND_LIVE@ june.ui=gui

label june-text
	menu label Install June Local ${JUNE_VERSION} - text mode (basic UI)
	linux @LINUX@
	initrd @INITRD@
	append @APPEND_LIVE@ june.ui=text
EOF

cat > config/bootloaders/grub-pc/config.cfg <<'EOF'
set timeout=3
set default=0

if [ x$feature_default_font_path = xy ] ; then
    font=unicode
else
    font=$prefix/unicode.pf2
fi

# Copied from the netinst image.  The same config also runs under legacy
# BIOS GRUB on June USB sticks, which has no efi_* modules.
if loadfont $font ; then
    set gfxmode=800x600
    set gfxpayload=keep
    if [ "$grub_platform" = efi ]; then
        insmod efi_gop
        insmod efi_uga
    fi
    insmod video_bochs
    insmod video_cirrus
else
    set gfxmode=auto
    insmod all_video
fi

insmod gfxterm
insmod png

source /boot/grub/theme.cfg

terminal_output gfxterm

insmod play
play 960 440 1 0 4 440 1
EOF

cat > config/bootloaders/isolinux/isolinux.cfg <<'EOF'
include menu.cfg
default vesamenu.c32
prompt 0
timeout 30
EOF

# June boot theme: the installer's dark background with ember accents.  The
# splash and menu replace Debian's defaults; binary_includes runs after
# binary_syslinux, so files under config/includes.binary win.
mkdir -p config/includes.binary/isolinux config/includes.binary/boot/grub/live-theme
cp /build/boot-theme/splash.png config/includes.binary/isolinux/splash.png
cp /build/boot-theme/splash800x600.png config/includes.binary/isolinux/splash800x600.png

cat > config/bootloaders/isolinux/stdmenu.cfg <<'EOF'
menu background splash.png
# syslinux colours are #AARRGGBB (alpha first), not #RRGGBBAA.
menu color title	* #fff89a4e *
menu color border	* #00000000 #00000000 none
menu color sel		* #ff0b0c0e #ffff7a2e *
menu color hotsel	1;7;37;40 #ff0b0c0e #ffff7a2e *
menu color tabmsg	* #ffffffff #00000000 *
menu color help		37;40 #ffdddd00 #00000000 none
menu vshift 14
menu rows 8
menu helpmsgrow 15
menu cmdlinerow 16
menu timeoutrow 16
menu tabmsgrow 18
menu tabmsg Press ENTER to boot or TAB to edit a menu entry
EOF

cat > config/bootloaders/isolinux/menu.cfg <<'EOF'
menu hshift 0
menu width 82
menu title June Local Installer
include stdmenu.cfg
include live.cfg
menu clear
EOF

cat > config/includes.binary/boot/grub/live-theme/theme.txt <<'EOF'
desktop-image: "/isolinux/splash800x600.png"
title-text: ""
message-font: "Unifont Regular 16"
message-color: "#8b909a"
terminal-font: "Unifont Regular 16"

+ boot_menu {
        left = 22%
        width = 56%
        top = 40%
        height = 45%
        item_color = "#c9ccd2"
	item_font = "Unifont Regular 16"
        selected_item_color = "#ffb04a"
	selected_item_font = "Unifont Regular 16"
        item_height = 20
        item_padding = 0
        item_spacing = 10
	icon_width = 0
	icon_height = 0
	item_icon_space = 0
}

#help bar at the bottom
+ label {
        top = 100%-50
        left = 0
        width = 100%
        height = 20
        text = "@KEYMAP_SHORT@"
        align = "center"
        color = "#6b7280"
	font = "Unifont Regular 16"
}

#progress bar
+ progress_bar {
        id = "__timeout__"
        left = 15%
        top = 100%-84
        height = 16
        width = 70%
        font = "Unifont Regular 16"
        text_color = "#0b0c0e"
        fg_color = "#ff7a2e"
        bg_color = "#23262c"
        border_color = "#23262c"
        text = "@TIMEOUT_NOTIFICATION_LONG@"
}
EOF

# June USB sticks (installer/usbtool) copy the ISO's files onto FAT32.  Give
# them the legacy BIOS boot code (built from this container's GRUB, so it
# matches the i386-pc modules beside it) and the release name.
mkdir -p config/includes.binary/boot/grub
bash /build/make_bios_boot.sh config/includes.binary/boot/grub
echo "$JUNE_VERSION" > config/includes.binary/june-version.txt

lb build

# GUI preflight: the graphical installer must not ship with a missing kiosk
# dependency. Debian bookworm's cage requires /usr/bin/Xwayland even though cog
# only uses Wayland; without it the kiosk falls back to the text UI.
gui_required="usr/bin/cage usr/bin/cog usr/sbin/seatd usr/bin/Xwayland usr/bin/june-cursor-nudge usr/share/icons/Adwaita/cursors/left_ptr"
for rel in $gui_required; do
  if [ ! -e "chroot/$rel" ]; then
    echo "BUILD CHECK FAILED: chroot/$rel is missing; the graphical installer would fall back to text." >&2
    exit 1
  fi
done
for rel in usr/bin/cage usr/bin/cog usr/sbin/seatd usr/bin/Xwayland; do
  if [ ! -x "chroot/$rel" ]; then
    echo "BUILD CHECK FAILED: chroot/$rel is not executable." >&2
    exit 1
  fi
done
if ! grep -q 'cog --platform=wl' chroot/usr/local/bin/june-kiosk; then
  echo "BUILD CHECK FAILED: kiosk command is missing cog --platform=wl." >&2
  exit 1
fi
if ! grep -q 'Xwayland' chroot/usr/local/bin/june-kiosk; then
  echo "BUILD CHECK FAILED: kiosk is missing the Xwayland preflight." >&2
  exit 1
fi
if ! grep -q 'WLR_NO_HARDWARE_CURSORS' chroot/usr/local/bin/june-kiosk; then
  echo "BUILD CHECK FAILED: kiosk is missing the software-cursor setting." >&2
  exit 1
fi
if ! grep -q 'XCURSOR_THEME' chroot/usr/local/bin/june-kiosk; then
  echo "BUILD CHECK FAILED: kiosk is missing the cursor theme setting." >&2
  exit 1
fi
if ! grep -q 'video render' chroot/usr/lib/live/config/0040-june-seat; then
  echo "BUILD CHECK FAILED: live seat hook is missing video/render group setup." >&2
  exit 1
fi
echo "[+] GUI preflight OK: cage, cog, seatd, Xwayland present"

ISO="$(ls -1 live-image-amd64.hybrid.iso)"
echo "OK -> $ISO"
