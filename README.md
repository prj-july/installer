# June Local — installer

Source for the June Local installer: a minimal Debian live image that
provisions a June oven to talk to a local June server instead of June's cloud.
It flashes the oven over USB (RCM/UMS), installs the local service and
certificates, and can back up or restore the oven's eMMC. The recommended
install is **Connect by IP**: a per-home CA whose certificate includes the
oven's IP, so there is no hostname or DNS to set up. **Connect by name
(june.local)** adds the project certificate authority and covers `june.local`,
`june2.local` and `june3.local`; the JuneDns helper advertises `<name>.local`,
and if multicast (mDNS) is blocked you can add a local DNS entry or use
**Connect by IP** instead.

> **Beta (0.8).** The installer is ready for testers. Make a backup of the
> oven first: the installer can restore it if anything goes wrong. Report how
> it went in [issues](https://github.com/prj-july/installer/issues) or at
> <hello@project-july.org>. The step-by-step guide is at
> <https://project-july.org/installer/>.

Release notes for the current build: [`RELEASE_NOTES.md`](RELEASE_NOTES.md).

**Factory reset:** June Local re-creates the oven's local identity on the next
boot, so a reset no longer leaves the oven unable to connect. A reset still
wipes the paired-client roster and `/data` (June Local logs and media), so
re-pair your phone/Home Assistant afterwards. An SOS "Factory Reset" may also
restore the stock system, in which case re-run the installer.

**Connect by IP** is the intended everyday install: it creates a per-home CA and
a certificate that includes the oven's IP, so the phone can use the IP directly
with no hostname/DNS. **Connect by name (june.local)** is the name-based
alternative: it adds the project CA to the oven's trust store and covers
`june.local`/`june2.local`/`june3.local`; the JuneDns helper advertises
`<name>.local` — if multicast (mDNS) is blocked, add a local DNS entry or use
**Connect by IP** instead. In both cases the phone must install the CA before
it will trust the oven — download it from `http://<oven-ip>/local/ca.crt` after
the install (see [`USE_LIVE_INSTALLER.md`](USE_LIVE_INSTALLER.md)).

Project July is an independent community project, not affiliated with or
endorsed by June Life or Weber (see [`NOTICE.md`](NOTICE.md)). June's APKs,
firmware and private keys are **not** included in this repository; see
[`installer/liveiso/PUBLISHING.md`](installer/liveiso/PUBLISHING.md) for the full
input list and what must be supplied locally.

## Make the installer USB stick

The installer runs on a laptop, started from a USB stick of 16 GB or more
(32 GB recommended). The easiest way to make the stick is **June USB**, a
small app for [Windows](https://downloads.project-july.org/june-usb/June-USB.exe)
and [Mac](https://downloads.project-july.org/june-usb/June-USB.dmg) (macOS 13 or later). It
downloads the latest installer, checks its SHA-256, and prepares the stick:

1. **Get the installer**: *Download and continue* (or use an ISO you already have).
2. **Choose the USB stick**: only USB drives are listed; built-in disks never are.
3. **Confirm**: tick that you've copied off what you need, then confirm the
   system dialog that names the stick. Making the stick erases it.
4. **Wait** a few minutes while it writes and checks every file. It ejects the
   stick when it's done.

<img src="website-assets/screenshots/june-usb/02-choose-stick.png" width="600" alt="June USB: Choose the USB stick">

The stick boots with one menu and Secure Boot on, and keeps the rest of its
space (`JUNE`) for oven backups, logs and certificates. Running June USB again
updates the installer and keeps those.

**Alternatives:** [Ventoy](CREATE_VENTOY_USB.md) (also the way on Linux), or
the [installer image](https://downloads.project-july.org/installer/latest) on
its own. The full guide, with every screen, is [`CREATE_USB.md`](CREATE_USB.md).
Then follow [`USE_LIVE_INSTALLER.md`](USE_LIVE_INSTALLER.md).

## Layout

```
installer/liveiso/   build the ISO (Docker + live-build) and the installer menu
installer/backup/    june-backup: verify oven images and dry-run restore
installer/usbtool/   June USB: Windows/Mac app (and Linux CLI) that makes the
                     installer USB stick
golocal/             Go source for june-local and june-camerad (make oven,
                     make camerad); prebuilt binaries are not committed
onoven/              on-oven deploy scripts
                     deploy_onoven.sh, june_bootclear.sh.onoven,
                     june_reaper.sh.junelocal, oven-light/ (interior-light
                     bridge APK and smali source), june-dns/ (mDNS helper APK
                     source, built Arch-side)
packaging/           certificate generation (generate_certs.sh, sign_leaf.sh)
tools/orchestrator/  boot image builder (build_boot_junelocal_init.py) and the
                     jcm_apply.sh fallback service
apk-patches/         APK patch recipes (jcm/remotes/mediacapturer/analytics
                     no-pin, chef-cookmodes remote primitive whitelist +
                     plan_index)
```

Point `prepare_assets.sh` at this checkout with `JUNE_LR` (it defaults to the
repo root) plus the locally supplied blobs.

The camera/live-view/interior-light architecture is summarized in
[`CAMERA.md`](CAMERA.md).
How to make the installer USB stick (the June USB app, with Ventoy and the
plain ISO as alternatives) is in [`CREATE_USB.md`](CREATE_USB.md); the Ventoy
recipe in detail is in [`CREATE_VENTOY_USB.md`](CREATE_VENTOY_USB.md).
The live installer walkthrough (basic/custom install, backup, restore, phone
UI, text mode) is in [`USE_LIVE_INSTALLER.md`](USE_LIVE_INSTALLER.md).
Website tutorial screenshots and mockups are in [`website-assets/`](website-assets/)
(SVG source, 1920 px PNGs, and an `index.html` preview).

## Build

`prepare_assets.sh` assembles the payload from this tree plus a few blobs that
are not in git (June APKs, certificates/keys, firmware, prebuilt armv7
binaries, the flashed boot image). See
`installer/liveiso/PUBLISHING.md` for the exact list.

It also builds the per-oven identity seed template
(`assets/accounts_template.db`) from a captured oven `accounts.db` (python3 is
required). The installer mints a fresh token per oven and june_reaper installs
the account row before Android starts when a factory reset wiped it; without
the template the rest of the install still works but that reset self-heal is
unavailable. Point `JUNE_ACCOUNTS_TEMPLATE` at the captured DB if it is not in
the default location.

```sh
cd golocal && make oven camerad && cd ..   # build the armv7 binaries
cd installer/liveiso
JUNE_LR=<this-repo> JUNE_ROOT=<tree-with-tools/certs> bash prepare_assets.sh
bash build_live_iso.sh          # writes june-local-live.iso
```

`build_live_iso.sh` uses Docker (legacy builder) to run live-build. Put the
resulting ISO on a stick with June USB (**Use an ISO file I already have**) or
Ventoy (`flash_to_ventoy.sh`), boot the target machine from it, and follow the
on-screen menu. `publish_release.sh` uploads a release and updates
`latest.json` and `releases.json` (see `installer/liveiso/PUBLISHING.md`).

Remote toast levels and the other non-bake primitives need the optional Chef
patch. Build it from your own extracted Chef APK with
`apk-patches/chef-cookmodes/build_patched.sh`; `prepare_assets.sh` picks up
`Chef_cookmodes_signed.apk` when it is present and the installer stages it
through the same boot-time applier as the other patched priv-apps. Without it
the installer leaves the stock Chef whitelist in place.

## Not included

- June APKs (`JuneCloudMessaging.apk`, `Remotes.apk`, companion endpoint)
- June's TLS key and the prepackaged GoDaddy chain
- NVIDIA/June firmware blobs (`tegrarcm`, `jetson-tk1.bct`, `u-boot-dtb-tegra.bin`)
- Prebuilt armv7 binaries (`june-local`, `june-camerad`) and the boot image
- The stock and patched Chef APK (`Chef.apk` / `Chef_cookmodes_signed.apk`)

The recipes in `apk-patches/` rebuild the patched apps from APKs you extract
from your own oven; keys and blobs stay on your machine.

## Certificates

[`packaging/june-standard-ca.crt`](packaging/june-standard-ca.crt) is the
Project July CA (`CN=*.junelife.com`, backdated to 2000). The standard install's
server certificate is signed by it, for `api.junelife.com`,
`messaging.junelife.com` and `june.local`, `june2.local`, `june3.local`. Install
it on a phone or other client that should trust a standard-install oven. It is
only the public certificate; its key is in neither this repository nor the
ISO. Custom installs use a per-home CA from `packaging/generate_certs.sh`
instead.

## License

MIT, see [`LICENSE`](LICENSE). The bundled Barlow fonts are under the SIL Open
Font License; [`NOTICE.md`](NOTICE.md) lists third-party components and the
June trademark notice.
