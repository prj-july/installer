# Creating the June Local Ventoy USB stick

This is the Ventoy recipe for the USB stick that boots the June Local
installer. It is written so a website author can turn it into a tutorial.

> On Windows and Mac, the **June USB** app is the easier way: it downloads and
> checks the installer and prepares the stick by itself, with one boot menu and
> no Secure Boot key to enroll. See [`CREATE_USB.md`](CREATE_USB.md). Ventoy
> remains the way to make the stick on Linux, and an alternative everywhere.

## What the user needs

- A USB flash drive. **16 GB minimum, 32 GB recommended** if the same stick
  will hold an oven backup. USB 3 is strongly preferred.
- A Windows or Linux computer. Windows users can use the Ventoy GUI; Linux
  users use `Ventoy2Disk.sh`.
- Ventoy from the official site: <https://www.ventoy.net>.
- The latest `june-local-live.iso` from
  <https://downloads.project-july.org/installer/latest> (the website should
  link it and show the current SHA256).
- Optional: a second USB drive for the oven backup if the Ventoy stick does
  not have enough free space.

## Project-specific rules (do not skip)

1. **Keep the Ventoy data partition label `Ventoy`.** The installer looks for
   this label (`ventoy`, case-insensitive) to find the stick for logs,
   certificates, and the backup target.
2. **Use exFAT (Ventoy default) or NTFS, never FAT32.** The oven backup is
   about 7.3 GB; FAT32 cannot store files larger than 4 GB. The installer
   explicitly skips filesystems that cannot hold a >4 GB backup.
3. **Leave at least ~8 GB free on the stick** if it will receive the oven
   backup. If it is nearly full, use a second USB drive.
4. **The live installer runs from RAM (`toram`).** After it has booted, the
   boot USB can be removed and a different USB drive can be plugged in for the
   backup. Do not create a Ventoy persistence partition; it is not needed.
5. **Ventoy is the first boot menu; June Local is the second.** The Ventoy
   menu selects the ISO. The June Local ISO then shows its own menu with the
   graphical installer (default) and the basic text UI. Both stages are
   expected.
6. **Secure Boot must be handled.** Either enroll Ventoy's key on first boot
   or disable Secure Boot in the firmware. MBR partition style is recommended
   for maximum compatibility; GPT also works.

## Step 1 — Download and verify

1. Download Ventoy from <https://www.ventoy.net> (official download).
2. Download the latest `june-local-live.iso` from
   <https://downloads.project-july.org/installer/latest>.
3. Verify the ISO before copying it:

   Windows PowerShell:

   ```powershell
   Get-FileHash .\june-local-live.iso -Algorithm SHA256
   ```

   Linux:

   ```sh
   sha256sum june-local-live.iso
   ```

   Compare the result with the hash shown on the release page and in
   <https://downloads.project-july.org/installer/latest.json> (the same values
   June USB checks). The 0.9.0 release is tagged `v0.9.0`; its build id and SHA256
   are published with the release. Never trust a hash from anywhere else.
   The website should always show the current release's hash.

## Step 2 — Install Ventoy (Windows)

1. Back up anything already on the USB drive. Installing Ventoy erases it.
2. Extract the Ventoy Windows ZIP and run `Ventoy2Disk.exe` as Administrator.
3. Select the correct USB device. Check the model and size twice. Ventoy lists
   USB drives by default; leave "Show all devices" off unless you are certain.
4. Open **Option → Partition Style** and choose **MBR** (recommended). Enable
   **Secure Boot Support** only if the target computer has Secure Boot enabled
   and you want to enroll Ventoy's key.
5. Click **Install** and confirm the data-loss warning. Wait for completion.
6. The USB now has two partitions:

   - a small 32 MB `VTOYEFI` partition (do not touch it);
   - a large exFAT partition labeled `Ventoy`.

   Keep the label and filesystem as they are.

## Step 2 — Install Ventoy (Linux)

1. Install the filesystem tools Ventoy needs. On Debian/Ubuntu:

   ```sh
   sudo apt install dosfstools exfatprogs
   ```

   Older distributions may call the exFAT package `exfat-utils`. This avoids
   the common `mkfs.vfat: command not found` error.
2. Extract the Ventoy Linux tarball.
3. Identify the USB device carefully:

   ```sh
   lsblk -o NAME,SIZE,MODEL,TRAN,MOUNTPOINTS
   ```

   Use the whole-disk device (for example `/dev/sdX`), never a partition.
4. Install Ventoy. This erases the USB:

   ```sh
   sudo sh Ventoy2Disk.sh -i /dev/sdX          # MBR (recommended)
   # add -s for Secure Boot support
   # use -g for GPT only if you specifically need it
   ```

   The default label is `Ventoy`; do not change it.
5. The USB now has the same two partitions as the Windows result.

## Step 3 — Copy the ISO

1. Mount/open the large exFAT partition labeled `Ventoy`.
2. Copy `june-local-live.iso` to the root of that partition. The root is the
   simplest choice; a subdirectory also works if you update the config path.
3. Optionally verify the hash of the copied file again.
4. Safely eject the USB.

## Step 4 — Recommended Ventoy config

Create `ventoy/ventoy.json` on the Ventoy partition (create the `ventoy`
folder if it does not exist):

```json
{
  "control": [
    { "VTOY_DEFAULT_MENU_MODE": "0" },
    { "VTOY_FILT_DOT_UNDERSCORE_FILE": "1" },
    { "VTOY_MENU_TIMEOUT": "3" },
    { "VTOY_DEFAULT_IMAGE": "/june-local-live.iso" }
  ]
}
```

- This selects the June Local ISO automatically after 3 seconds and hides
  macOS metadata files.
- The Ventoy timeout only controls the first menu. The June Local menu then
  appears and lets the user choose the graphical installer (default) or the
  basic text UI.
- If the ISO lives in a subdirectory, use that path, for example
  `"VTOY_DEFAULT_IMAGE": "/ISO/june-local-live.iso"`, and optionally add
  `"VTOY_DEFAULT_SEARCH_ROOT": "/ISO"`.
- To show the Ventoy menu instead, remove `VTOY_DEFAULT_IMAGE` and
  `VTOY_MENU_TIMEOUT`, or press a key during the timeout.

## Step 5 — Boot the installer computer

1. Plug the stick into the computer that will run the installer (the laptop
   that flashes the oven, not the oven itself).
2. Open the boot menu (usually F12, F11, Esc, or F2; it varies by vendor) and
   select the USB drive. Prefer the UEFI entry if both UEFI and Legacy entries
   are shown.
3. If Secure Boot is enabled:

   - With Ventoy Secure Boot support installed, the first boot may show
     MokManager. Choose **Enroll key from disk**, select the `VTOYEFI`
     partition, choose `ENROLL_THIS_KEY_IN_MOKMANAGER.cer`, confirm, and
     reboot.
   - Or disable Secure Boot in the firmware and boot again.

4. Ventoy appears, then auto-selects `june-local-live.iso`.
5. The June Local menu appears. Choose **graphical installer** (default) or
   **basic text UI**, then follow the installer instructions.

## Step 6 — Backups, logs, and certificates

- The installer lists the Ventoy stick as `Ventoy (current boot USB)` when it
  can be used as a backup target.
- Keep at least ~8 GB free for one oven backup. The backup is about 7.3 GB and
  can take 15–30 minutes at typical USB speeds.
- Logs are written to `june-logs/` on the Ventoy partition. Custom-install
  certificates are written to `june-certs/`.
- Because the live system runs from RAM, the boot USB can be removed after
  boot and a different USB drive can be plugged in for the backup. Wait until
  the installer confirms it is safe, then swap drives if you prefer.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `mkfs.vfat: command not found` on Linux | Install `dosfstools exfatprogs` (or `exfat-utils`) and rerun `Ventoy2Disk.sh`. |
| Ventoy does not list the ISO | Put the ISO on the large exFAT `Ventoy` partition, not the 32 MB `VTOYEFI` partition. Check the `.iso` extension and re-plug the stick. |
| Ventoy stick is not offered as a backup target | Keep the `Ventoy` label, use exFAT/NTFS (not FAT32), ensure it has free space and is writable. If it still does not appear, use a second USB drive. |
| Secure Boot verification error | Enroll Ventoy's key with MokManager, or disable Secure Boot. |
| A Surface (or another Secured-core laptop) skips the stick or says it isn't bootable | First set Secure Boot to **Microsoft & 3rd party CA** in the firmware; see [`CREATE_USB.md`](CREATE_USB.md#surface-and-other-secured-core-laptops). ARM laptops (Surface Pro 9 with 5G, Surface Pro X) can't start the installer. |
| `No bootable device` | Check the boot order, try the other USB entry (UEFI vs Legacy), and confirm Ventoy installed both partitions. |
| Two menus confuse users | That is expected: Ventoy first, June Local second. Set `VTOY_MENU_TIMEOUT` to 3 and keep the default image. The June Local menu is where graphical vs text is chosen. |
| Very slow backup | Use a USB 3 port and stick. The oven's eMMC reads at roughly 8–10 MB/s, so a full backup can take 15–30 minutes. |
| Ventoy update | Updating Ventoy preserves the data partition. Use the same or newer version and do not reformat the `Ventoy` partition. |

## Checklist for the website tutorial

- Show the finished stick with the two partitions (`VTOYEFI` + exFAT
  `Ventoy`) and the `Ventoy` label.
- Show `june-local-live.iso` on the Ventoy partition.
- Show the `ventoy/ventoy.json` contents.
- Show the first Ventoy menu and the June Local menu (graphical default and
  basic text).
- Show the Secure Boot enrollment or disable step.
- Explain `june-logs/`, `june-certs/`, the ~7.3 GB backup size, and the
  option to swap USB drives because the live system runs from RAM.
- Show the current release's ISO SHA256 and tell users to verify it.
- Warn that installing Ventoy erases the USB, and that the installer modifies
  the oven's eMMC.

## Next

For what the live installer looks like and how to use each job, see
[`USE_LIVE_INSTALLER.md`](USE_LIVE_INSTALLER.md).
