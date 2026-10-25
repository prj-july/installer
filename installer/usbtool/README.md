# June USB

A small Windows/macOS app (plus a Linux command line) that turns a USB stick
into a June Local installer stick. No Ventoy needed. It downloads the latest
`june-local-live.iso`, checks its SHA256, and lays the stick out as:

| | Partition | Format | Label | Contents |
|---|---|---|---|---|
| sectors 0–2047 | — | — | — | GRUB legacy-BIOS boot code (`june-bios.img` from the ISO) |
| 1 | 4 GiB, active, type `0x0C` | FAT32 | `JUNEBOOT` | the ISO's files: UEFI (shim + signed GRUB, Secure Boot works) and BIOS boot |
| 2 | rest of the stick, type `0x07` | exFAT | `JUNE` | oven backups, `june-logs/`, `june-certs/` |

- **One boot menu**: the firmware starts the ISO's own GRUB directly.
- **Updating a stick only replaces `JUNEBOOT`**, so backups on `JUNE` survive.
- 16 GB minimum (installer plus one ~8 GB oven backup), 32 GB recommended.

## Safety

- Built-in disks (NVMe, SATA, RAID…) are never listed, and the system or
  boot disk is never listed on any bus. By default only USB drives up to
  300 GB are shown.
- Erasing needs a ticked "I have copied everything I need from “LABEL”"
  plus a system dialog naming the stick. `ConfirmErase` in `app.go` has to
  say yes before `Prepare` will erase anything.
- The drive is checked again right before erasing (model, size, serial), in
  case the stick was swapped.
- Every copied file is read back and compared by SHA256, bypassing the file
  cache on Windows. The boot code is read back too.
- The boot code writer refuses to run unless the stick has the expected MBR
  layout (`stick/bootcode.go`, unit-tested).

## Layout of the code

- `stick/`: everything that touches disks. One file per platform:
  `platform_windows.go` (Storage cmdlets + diskpart), `platform_darwin.go`
  (diskutil; the boot-code step runs as root through the standard password
  prompt), `platform_linux.go` (sfdisk, mkfs; used by the CLI and tests).
- `main.go`, `app.go`: the Wails app (Windows/macOS) that `frontend/` talks to.
- `frontend/`: plain HTML/CSS/JS, no build step. `app.css` and `fonts/` are
  copies of the installer's web UI (`installer/liveiso/gui/web`); run
  `./sync-ui.sh` after changing those. Served by any static server and
  opened with `?mock` (e.g. `http://localhost:5174/?mock`), `usb.js` runs
  on a labelled mock backend with fake drives, for designing the screens.
  Without `?mock` it only talks to the real Go side.
- Debugging: `go build -tags desktop,production,junedebug -ldflags -H=windowsgui .`
  logs every backend call to `june-usb-debug.log` and Wails' own log to
  `june-usb-wails.log` in the temp folder (`debug_on.go`).
- `cmd/june-usb-cli/`: the same flow on the command line.
- The ISO side lives in `installer/liveiso`: `make_bios_boot.sh` builds
  `/boot/grub/june-bios.img` and `/boot/grub/i386-pc/`, and `build.sh`
  writes `/june-version.txt`. ISOs without them still work (UEFI only, the
  version shown as the build date).

## Build

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
wails build -platform windows/amd64 -skipbindings   # build/bin/June-USB.exe
wails build -platform darwin/universal -skipbindings # on a Mac: build/bin/June-USB.app
go test ./stick
```

Releases come from `.github/workflows/june-usb.yml` (tag `usb-vX.Y`); see
[SIGNING.md](SIGNING.md).

## macOS: "Writing the boot code" error after the copy (fixed)

macOS builds before this fix could stop at the last step, after all 842 files
had been copied and verified, with:

```text
Writing the boot code: 0:347: execution error: sync /dev/rdisk4: inappropriate ioctl for device (1)
```

`fsync(2)` is not implemented for raw disks on macOS: `/dev/rdisk*` returns
`ENOTTY` even though the write already reached the stick. The app treated that
as a failure, so it reported the run as failed and skipped the eject. No data
was damaged and nothing needs to be re-copied.

The copied installer files are complete and verified by SHA256 before the boot
code is touched. The boot code only adds legacy-BIOS support: a stick from a
failing run still starts on UEFI computers (every Mac and any PC from the last
decade). Only old BIOS-only PCs need the boot code.

Current builds fix this: the boot code is still read back and verified, an
unsupported `fsync` is no longer an error, and the stick is flushed with
`sync(2)` and ejected instead. If you hit this on an older build, update the
app and run it again (Update mode keeps the `JUNE` partition), or use a
known-good Ventoy stick on BIOS-only machines (Ventoy boots both UEFI and
legacy BIOS). Eject the stick from Finder before unplugging it after a failing
run, since the automatic eject was skipped.

## Test without a real stick (Linux/WSL, as root)

```sh
truncate -s 16G stick.img && L=$(losetup -fP --show stick.img)
JUNE_USB_ALLOW_LOOP=1 ./june-usb-cli -device $L -iso june-local-live.iso -yes
losetup -d $L
# boot it: SeaBIOS
qemu-system-x86_64 -m 4096 -drive if=none,id=u,format=raw,file=stick.img -device qemu-xhci -device usb-storage,drive=u
```

On Windows, `JUNE_USB_ALLOW_VHD=1` lists attached VHDs.

## Release manifest

The app reads `https://downloads.project-july.org/installer/latest.json`:

```json
{"version": "v0.8.1", "url": "https://downloads.project-july.org/installer/v0.8.1/june-local-live.iso",
 "sha256": "<the published sha256>", "size": <bytes>}
```

It also reads `https://downloads.project-july.org/installer/releases.json`
(every published version, newest first; see `installer/liveiso/PUBLISHING.md`)
and offers the last three: the latest is preselected, older ones are folded
away under it. An ISO the user picks is verified if it matches any listed
version. Without `releases.json` the app offers `latest.json` alone.

Downloads are cached per version (`%LOCALAPPDATA%\June USB\<version>\`,
`~/Library/Caches/June USB/<version>/`). A cached ISO is checked against its
SHA256 and reused. A download that stops is kept as `.part` and continued
next time with a range request; the SHA256 still covers the whole file, and a
part that does not match is deleted. After a download, ISOs of versions the
app no longer offers are removed.

`JUNE_USB_MANIFEST` and `JUNE_USB_RELEASES` override the URLs for testing.
Without a manifest, users can still pick an ISO they downloaded; it is then
shown as not verified.
