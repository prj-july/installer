# Using the June Local live installer

This is the companion to `CREATE_USB.md`: what the live installer looks
like and how to use each job. The screenshots below are real/mockup assets in
`website-assets/`; replace the mockups with the phone screenshots from
`website-assets/screenshots/` when they are available.

## Before you start

- The USB stick from `CREATE_USB.md` (made with June USB, or with Ventoy).
- The laptop that will run the installer.
- The oven, its power cable, and the USB cable used for RCM/UMS.
- Optional: a phone on the same network as the laptop.
- Time: an install takes about two minutes; a backup or restore takes
  15–30 minutes depending on the drive.

The installer has two interfaces:

- **Graphical installer** — the normal path (recommended).
- **Basic text UI** — the fallback if the graphical interface cannot start.

Both do the same jobs.

## 1. Boot the installer

1. Boot the laptop from the stick. A June USB stick goes straight to the June
   Local menu. On a Ventoy stick, Ventoy's menu appears first and boots
   `june-local-live.iso` after its timeout.
2. The June Local menu appears. Choose **Graphical installer (default)** unless
   you are debugging, in which case choose **Basic text UI**.
3. The installer starts and shows the home screen.

Assets: `website-assets/png/01-overview-flow.png`,
`website-assets/png/06-boot-menus.png`.

## 2. The home screen

The heading is **“What would you like to do?”**. There are five tiles:

| Tile | What it does |
| --- | --- |
| **Install June Local** | Recommended. Installs the local server, camera bridge, and project certificate. The factory June app keeps working, but talks to June Local. |
| **Install with your own CA** | Advanced. Creates a private certificate authority on the USB and signs a certificate for the oven’s address. You install the CA on the phone afterwards. |
| **Back up the oven** | Makes a full ~7.8 GB image of the oven’s storage on a USB drive. |
| **Restore a backup** | Puts a backup image back on the oven. This replaces everything on the oven. |
| **More / Phone setup** | Setup guide, text mode, restart, or phone access. |

The status line at the top says whether the oven is connected:

- **No oven connected yet** — normal before RCM.
- **Oven connected · RCM mode** — ready to install/restore.
- **Oven connected · storage open** — ready for UMS operations.

Asset: `website-assets/png/09-installer-ui.png`.

## 3. Basic install (recommended)

### Options

Choose **Install June Local**. The options screen says:

> **Install June Local on this oven**
> The oven keeps June's certificate and its factory messaging app, so the June
> app works as before, but talks to June Local instead of June's cloud.

**Keep the no-pin patch after a factory reset** is off by default. Turn it on
only if you want the no-pin patch to survive a factory reset; it stores the
patched app in `/system`.

Press **Next: connect the oven**.

Asset: `website-assets/png/11-install-options.png`.

### Connect the oven

The screen is **“Put the oven in recovery mode”**:

1. Connect the oven to the laptop with the USB cable. Use a port on the laptop
   itself, not a hub.
2. Press and hold **SW2** (force recovery) on the oven’s board.
3. Switch the oven’s power on and keep holding SW2 for about 10 seconds.

The panel updates itself. When it says **Oven found · RCM mode — you can let
go of SW2**, press **Install June Local**.

The oven’s fans spin up and stay fast while it works. That is normal.

Asset: `website-assets/png/12-connect-oven.png`.

### Progress

The running screen shows:

- a percentage, or elapsed time while the installer is still estimating;
- the current step and an ETA/estimating label;
- a progress bar and the written GiB/MB/s;
- a step tracker (Prepare → Connect oven → Write image → Verify → Finish);
- a **Show details** panel with the live log.

**Keep the oven connected and powered. Don’t unplug anything.** An install is
usually about two minutes.

Asset: `website-assets/png/13-progress.png`.

### Success

When it finishes, the screen says **“June Local is installed”** and shows:

1. Unplug the oven’s power and the USB cable.
2. Wait 10 seconds, plug the power back in and let the oven start normally.
3. From a device on the same network, open **`http://<oven-ip>/local`**. You
   should see the June Local status page.
4. Pair from the June app or Home Assistant. Then close up the oven.

If you chose persistence, the no-pin patch is staged in `/system` and applied
on the oven’s next start.

Asset: `website-assets/png/14-success.png`.

## 4. Custom install (your own CA)

Choose **Install with your own CA** when you specifically want a per-home
certificate authority.

1. Enter the **Oven LAN IP** (for example `192.168.1.50`) and a **Hostname**
   (the current UI prefills `oven.local`; use `june.local` or your own name if
   you prefer). Reserve the IP for the oven in your router.
2. The installer creates the CA on the USB stick and signs a certificate for
   the address you entered.
3. Continue through **Connect the oven** and the progress screen exactly as in
   the basic install.
4. On success, download the CA and the companion app from the phone QR code
   (or use the files on the USB). Install the CA on the phone:
   **Settings › Security › Install a certificate › CA certificate**.
5. Pair the phone app using the oven address.

The generated CA and leaf files are saved under `june-certs/` on the stick's
data partition (`JUNE` on a June USB stick, `Ventoy` on a Ventoy stick). The custom install also has the **Keep the patch after a factory
reset** option.

Assets: `website-assets/png/11-install-options.png`,
`website-assets/png/18-basic-vs-custom.png`.

## 5. Back up the oven

1. Choose **Back up the oven**.
2. The destination screen asks **“Where should the backup go?”** and lists
   usable drives:
   - **June USB (current boot USB)** or **Ventoy (current boot USB)** — the
     stick you booted from, if it has free space;
   - a second USB drive;
   - other safe internal/external drives when available.

   The backup is one ~7.8 GB file. The live system runs from memory, so you can
   unplug the boot stick and use another drive.
3. Pick a drive, optionally choose a folder (default `june-backups`) and a file
   name (prefilled with a timestamp).
4. Press **Next: connect the oven**, put the oven in RCM mode as above, and
   press **Start backup**.
5. The progress screen shows GiB, MB/s, ETA, and the tracker. A backup takes
   roughly 15–30 minutes at typical USB speeds.
6. On success, the installer writes the `.img` file and a `.sha256` file next
   to it (the checksum file names the image by its basename, so it still
   checks after the stick is remounted). Safely unplug the drive, then unplug
   the oven’s power and USB cable.

You can check a backup later without the oven:

```sh
backup.sh verify /path/to/261012-1200.img
```

Logs are saved to `june-logs/` on the stick's data partition (`JUNE` or
`Ventoy`).

Assets: `website-assets/png/15-backup-destination.png`,
`website-assets/png/13-progress.png`.

## 6. Restore a backup

Restoring replaces everything on the oven with the backup image. The installer
checks the image size and SHA256 *before* it writes, and refuses if the file
is the wrong size, the `.sha256` sidecar is missing, or the digest does not
match. Type **RESTORE** is still required; a dry-run of the same checks runs
first so a damaged backup never reaches the oven.

1. Choose **Restore a backup**.
2. Pick the drive that holds the backup image, press **Find backups**, and
   select the `.img` file.
3. Type **RESTORE** to confirm. The warning explains that June Local and any
   changes since the backup are removed.
4. Press **Next: connect the oven**, put the oven in RCM mode, and press
   **Erase oven and restore**.
5. Wait for the verify step, then the write. Do not unplug the oven.
6. On success, unplug the oven’s power and USB cable, close up the oven, then
   switch it on normally.

To check a restore without writing (no oven needed for the image checks):

```sh
backup.sh restore-dry /path/to/261012-1200.img
```

## 7. Use your phone

From the home screen, choose **Use your phone instead** or the **Phone** button
in the header:

- the laptop and phone must be on the same network;
- scan the QR code, or type the shown `http://<laptop-ip>/` address;
- the phone shows the same screens and can start and follow jobs;
- the phone can download the custom-install CA and the companion app;
- **Allow a phone on this network** must be on; anyone who scans the code can
  control the installer, so turn it off when you are done.

The phone UI is also the easiest way to take real screenshots for the website.

Asset: `website-assets/png/16-phone-access.png`.

## 8. Basic text UI

If the graphical installer cannot start, choose **Basic text UI** in the
June Local boot menu. The menu has:

```
1) Install June Local
2) Install with your own CA
3) Back up the oven
4) Restore a backup
5) Exit installer and reboot
```

The jobs are the same, the logs still go to `june-logs/` on the USB, and you
can reboot into the graphical installer later.

Asset: `website-assets/png/17-text-mode.png`.

## 9. Reading the progress screen

- **Percent** means the write/verify progress is known.
- **Elapsed time** means the installer is still estimating.
- **GiB and MB/s** are the amount written and the current speed.
- **Tracker** shows the current step; a failure marks the failed step with `!`.
- **Show details** shows the live installer log. The full log is also written
  to `june-logs/` on the USB.
- **Stop waiting** only appears when the installer is waiting for the oven; do
  not cancel a write or restore.
- The oven fans run fast while the job is active. That is normal.

Asset: `website-assets/png/13-progress.png`.

## 10. If something goes wrong

The failure screen shows the reason, a **Try again** button, and a **Back to
start** button. The details panel and the USB log show the last steps.

| Symptom | What to check |
| --- | --- |
| “Waiting for the oven” never changes | USB cable seated in the laptop (not a hub), SW2 held while powering on, oven power on. Try **Start without detecting** only if you know the oven is in RCM. |
| No usable backup drive | Drive is exFAT/NTFS/ext4 and writable; at least 8 GB free; rescan. |
| Wrong size on a restore image | The image is not a complete 7.8 GB oven backup. |
| “This backup has no SHA256 file” | The `.sha256` sidecar is missing. Make a new backup, or run `backup.sh checksum` on the `.img` only if you trust that copy. |
| “The backup file does not match its SHA256” | The image is damaged or incomplete. Do not restore it. |
| Secure Boot verification error | Ventoy sticks only: enroll Ventoy’s key or disable Secure Boot. June USB sticks start with Secure Boot on. |
| Graphical installer falls back to text | On builds before 2026-10-05 the kiosk log showed `Cannot find Xwayland binary "/usr/bin/Xwayland"`; the current image includes Xwayland and retries the software renderer. Update the USB image, or use the text UI on the older stick. |
| Job failed part-way | Do not unplug the oven; use **Try again**. If it repeats, save `june-logs/` and ask for help. |

Assets: `website-assets/png/10-troubleshooting.png`,
`website-assets/png/14-success.png`.

## 11. After the install

- The oven reboots normally into June’s UI.
- Open `http://<oven-ip>/local` from any device on the network to confirm
  June Local is running.
- Pair from the June app or Home Assistant.
- Check the Project July clients page for the companion app and the Home
  Assistant integration.
- Only close up the oven after the status page and pairing work.

## 12. Safety checklist

Asset: `website-assets/png/19-safety-checklist.png`.

- Use a USB 3 port on the computer itself, not a hub.
- Keep the oven powered and connected for the whole job.
- Wait for the success screen before unplugging anything.
- Let the fans spin fast; that is normal.
- Copy `june-logs/` if you need support.
- Do not unplug the oven during an install or restore.
- Do not use FAT32 for the backup drive.
- Do not rename the stick's partitions (`JUNEBOOT`/`JUNE`, or `Ventoy`).
- Do not close up the oven before testing it.

## Real screenshots

Drop the phone/installer screenshots into
`website-assets/screenshots/` using the names in
`website-assets/screenshots/README.md` (home, install options, connect oven,
progress, success, backup destination, phone access, failure, text mode). We
will replace the matching mockups and update the captions.
