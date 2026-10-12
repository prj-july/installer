# June Local live ISO

Builds a bootable Linux USB image that contains the installer so a Windows/macOS
friend only needs to flash one file and boot it.

## Build

```bash
cd installer/liveiso
# first copy the installer assets you want to ship into assets/
bash ./prepare_assets.sh
bash ./build_live_iso.sh
```

The host needs Docker or Podman (build runs `live-build` inside the container).

## Flash for friends

- **June USB is recommended** (`installer/usbtool`, see `CREATE_USB.md`): it
  makes a stick with the installer on a FAT32 `JUNEBOOT` partition and a
  writable exFAT `JUNE` partition, so the backup image lands on the same stick
  and shows up in Windows for recovery. Pass it a local build with
  **Use an ISO file I already have**.
- **Ventoy** does the same with its `Ventoy` data partition, and is the way
  to make the stick on Linux.
- Balena Etcher also works, but a plain Etcher flash is read-only; in that case
  keep a separate USB drive available for backups.
- Boot the laptop from the USB. The boot menu has two entries:
  - **June Local installer** (default after 5 seconds): the graphical
    installer, driven with the mouse.
  - **June Local installer (text mode)**: the `whiptail` menu, for machines
    where graphics do not work.
- The live system auto-logs in and opens the installer automatically. If the
  graphics cannot start, it falls back to the text menu by itself.
- If the menu is closed or you need a shell, run `june-menu` to reopen it.
  Ctrl+Alt+F2 gives a shell while the graphical installer runs.

## Graphical installer

`gui/` holds the graphical installer:

- `server.py`: a Python standard-library backend on port 80. It runs the same
  `install.sh` / `backup.sh` workers as the text menu and streams their
  progress to the page over server-sent events.
- `web/`: the page (plain HTML, CSS and JavaScript, no build step). Its look
  follows the July Oven card ("Still Glass": the oven's black glass, Barlow
  fonts, bundled under the SIL OFL so nothing loads from the internet).
- `june-kiosk`: runs `cage` + `cog` (WPE WebKit) fullscreen on tty1. It tries
  GPU rendering, then software (pixman), then falls back to the text menu.
- `june-gui.service`: the systemd unit for the backend.

**Phone access.** With a network cable plugged in, *Connect a phone* shows a
QR code. The phone gets the same screens, can start and follow jobs, and can
download the companion app (and the per-home `ca.crt`) straight from the
installer. Phone access is off until switched on; the code carries a random
token, and switching it off disconnects phones and makes a new code. Settings
that change the PC itself (text mode, restart, shut down, phone access) are
only available on the installer screen. Wi-Fi is not set up yet.

**Developing the UI** without an oven or a live USB:

```bash
bash installer/liveiso/gui/dev/run-mock.sh   # then open http://localhost:8765/
```

Mock mode simulates the oven and the workers (`gui/dev/mock_worker.py`); the
MOCK panel in the corner switches the oven on and off and makes the next job
fail. Open the page from another device (or this machine's LAN address) to see
the phone view.

## Notes

- The ISO is a minimal Debian live environment with no desktop: the graphical
  installer is one fullscreen page in a Wayland kiosk, not a desktop session.
- The text menu uses `whiptail` (a lightweight terminal UI).
- The installer auto-detects the June eMMC by size, explicitly skips the boot
  USB/system disk, and refuses to touch other disks.
- The menu also offers full eMMC **backup to USB** and **restore from USB**
  (the destination needs at least 8 GB free, and can be the boot USB itself:
  its `JUNE` or `Ventoy` data partition). Restore checks the `.sha256` sidecar
  and dry-runs before it writes; `backup.sh verify` and `june-backup` do the
  same checks without an oven (see `installer/backup/README.md`).
- Custom install now asks whether the JuneCloudMessaging patch payload should
  be staged in `/system` (persists across factory reset) or `/data` (does not).
  The app update is applied on the next boot by the June Local copy of
  `/system/bin/june_reaper.sh`: that is the stock script which mounts the
  encrypted `/system/priv-app` volume, and the June Local version mounts it
  read-write when a patch is staged, applies the patch before Android starts,
  then drops the one-shot `/data` staging copy. A status line is written to
  `/data/june/logs/jcm_patch.log` (world-readable). The boot image's
  `jcm_apply` service remains as a fallback; see
  [`docs/jcm-postboot-apply-runbook.md`](../../docs/jcm-postboot-apply-runbook.md).
- The standard install keeps the oven's factory JuneCloudMessaging app and
  never writes the factory APK under the patch name. Factory-reset persistence
  is opt-in there too (default off): when enabled, the no-pin JCM patch is
  staged in `/system` so it survives a factory reset.
- Both install flows ask for an oven name: the standard flow defaults to
  `june`, the custom flow to `june.local` (its IP field is prefilled with
  `192.168.1.`). The name is written to `/system/etc/june_hostname` and the
  boot script sets `net.hostname` from it, so Android advertises
  `<name>.local` over mDNS. Factory-reset persistence is a yes/no question and
  defaults to No.
- In the standard (prepackaged) install the name is mDNS-only: June's
  prepackaged certificate does not cover it, so HTTPS-by-name needs the custom
  (per-home CA) install, which puts the name in the certificate SAN.
- Optional for other devices on the network: point `api.junelife.com` and
  `messaging.junelife.com` at the oven's IP in local DNS (or a hosts file). The
  prepackaged certificate is a real certificate for those names, so even stock
  June clients verify there. The installer already writes the equivalent
  entries into the oven's own `/etc/hosts`.
- USB/oven connection guide: <https://project-july.org/setup>.
- `/opt/june-installer` on the live system contains `install.sh` and the deploy
  assets.
