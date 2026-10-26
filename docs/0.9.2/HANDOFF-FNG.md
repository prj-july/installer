# FnG on-oven test: June Local 0.9.2 test build

Prepared: 2026-10-26 (America/Los_Angeles)
Branch: `terablo/local-redesign` at `49154b7`
Version: `0.9.2-test` (test build, not a release)
Build id: `20261026-0227-fbe22473`

This is the ISO to install on FnG's oven **before 0.9.2 ships**. It contains the
new Chef "Project July" home-screen tile and the matching june-local oven page.

## Artifacts

| Artifact | Path | SHA-256 |
|---|---|---|
| Test ISO | `installer/liveiso/june-local-live-0.9.2-test-20261026-0227-fbe22473.iso` | `b74e948774056c350199a6a8c79e209a6979fabd0121be70ac25aa592a10103a` |
| Chef APK baked in | `installer/liveiso/assets/Chef_cookmodes_signed.apk` | `2062834c1a4b26aa4093946a42188e666ee5a127539b8df91a902feb424b5b8e` |
| june-local ARM binary baked in | `installer/liveiso/assets/june-local-linux-armv7` | `349c2a2c46ab8852a03fd7ddb928e3d7b4ed114eceae36a3d773613319af9a2f` |
| Wireless client baked in | `installer/liveiso/assets/june-wireless-update` | `d12e4414b74c4807f8a54479757e9ae36eef61d1db4cec33c0bc6e6d6ea22475` |

ISO size: `1,416,314,880` bytes.

## What is new

### Chef: a "Project July" tile on the home screen

The oven's real Settings screen is a separate app (`JuneSettings.apk`), so a
Chef-only patch cannot add a row there. The tile is on Chef's own launcher
instead: **home screen, next to Settings**, label **Project July**, icon
`launcher_chef`. Tapping it opens the existing full-screen WebView dialog on
`http://127.0.0.1/local/oven`.

- New Chef APK built from the stock APK in `/tmp` on the Linux host (the 0.9.0
  boot loop was caused by rebuilding on a case-insensitive filesystem).
- `AndroidManifest.xml` and `resources.arsc` are byte-identical to the clean
  0.9.1 cook-modes build (`8a006b80…`), so this is the same verified Chef plus
  dex changes only.
- Resource gate: 1610 zip entries, 614 `res/` entries, 614 `resources.arsc`
  paths, **0 missing**.
- Not the withdrawn foodack build. `0.9.2` stays on the clean cook-modes
  behavior; the foodack release remains withdrawn.

### june-local: version, phone-access toggle, update

`/local/oven` now shows:

- **June Local 0.9.2-test**, commit and build time, top-right and in the
  footer.
- The existing update flow: **Check now**, **Download and get ready**,
  **Restart to finish**, and **Go back to the previous app**.
- **Phone access to /local** card: On/Off, with a confirmation before turning
  it off and a **Turn phone access back on** button when off.
  - Stored in `/data/june/settings.json` (`local_ui_disabled`), so it survives
    a restart/reboot.
  - When off, a phone gets a 403 "June Local is turned off" page. The oven's
    own page is always reachable (loopback is exempt), so it can be turned back
    on from the oven.
  - Deliberate limitation: only the phone-facing `/local` HTML page is gated.
    `/local/status`, `/local/cook*`, `/local/ca.crt`, `/local/report*`,
    `/local/update*` and the one-time sign-in keep working so Home Assistant,
    reports and updates are not broken. A hard block of every phone endpoint
    would be a follow-up.

### Updater fix (required for the wireless update test)

v0.9.1's wireless updater was unusable on hardware: the static Go binary on
Android 5 had no DNS resolver and no usable CA store. This build fixes both and
surfaces them in status:

- **Android resolver**: reads `net.dns1..4` via `getprop`, falls back to
  `dhcp.*.dnsN`, `/etc/resolv.conf`, then the default gateway, and installs a
  pure-Go resolver before the updater starts.
- **Embedded CA roots**: `golocal/certs/roots.pem` (Mozilla-derived, includes
  GTS Root R4) is compiled in and used as the updater's `RootCAs`.
- `/local/status` now shows `network.dns`, `network.dns_source` and
  `network.ca_bundle`, and adds `update-dns` / `update-roots` problems when
  either is missing instead of leaving only a raw `last_error`.
- The update token and update-password QR are written to the **actual Ventoy
  mount** (`backup.sh ventoy-dir`, `/mnt/june-ventoy`), not only `/media/ventoy`.
- QR/update URLs use the oven's `.local` name. The basic certificate has no IP
  SAN, so `https://<oven-ip>` fails hostname verification.
- `min_sequence` stays `2026102501` so a 0.9.1 or 0.9.2 oven can be offered
  `2026102502`.

The 2026102502 release and the rebuilt client package are prepared in `dist/`
(see below).

### Signed release 2026102502 (upload before the update test)

- `dist/oven-updates-0.9.2/stable/` — release `2026.10.26-chef-cookmodes`,
  sequence `2026102502`, artifact = the same Project July Chef APK
  (`2062834c…`). Signed with the release key and verified with
  `june-update-publisher verify` for channel `stable`, target
  `june-oven-android5`.
- **Upload the whole `dist/oven-updates-0.9.2/stable/` directory to
  `https://downloads.project-july.org/oven-updates/stable/`** before running
  Check now. The live directory still has the older `2026102401` release;
  without this upload the oven cannot see 2026102502.
- `dist/oven-updates-0.9.1/stable/` (sequence `2026102501`, clean Chef) is the
  previously prepared release; `2026102502` supersedes it.
- `dist/june-wireless-update/` contains the rebuilt Linux and Windows clients,
  the updated README (use `https://<name>.local`) and `SHA256SUMS`.

## Test steps on the oven

**Do the backup first.** Use the June USB stick and a full eMMC backup; this
test replaces the installed Chef APK and the june-local binary.

1. Boot the test ISO, install with the standard flow (same options as usual),
   and let the oven reboot.
2. On the oven home screen, find the **Project July** tile (next to Settings)
   and tap it.
   - The page loads `/local/oven` and shows the June Local version/commit/build.
   - Back returns to the home screen.
3. Update flow (after the 2026102502 release is uploaded):
   - **Check now** must reach the manifest through the fixed resolver/roots.
     An oven at `2026102501` sees `2026102502` as update-available.
   - **Download and get ready**, then power-cycle. The boot applier verifies
     and applies the tile APK (already-applied if the baked bytes match), and
     **Check now** reports up-to-date at `2026102502`.
   - **Go back to the previous app**, restart, and **Check now** reports up to
     date for the undone release.
   - If Check now still fails, capture `/local/status` (`network.dns`,
     `dns_source`, `ca_bundle` and `problems`) before restoring; that is the
     evidence the DNS/roots fix needs.
4. Phone access:
   - The QR/one-time code opens `/local` on a phone, signed in; typing the code
     also works; the code works once.
   - Press **Turn off phone access** on the oven page, confirm, then reload
     `/local` on the phone: it shows the 403 "turned off" page.
   - Press **Turn phone access back on**; the phone can open `/local` again.
   - Reboot the oven and confirm the setting survived.
5. Normal oven behavior: start a normal cook, and check that a remote toast
   level still works (the cook-modes/plan-index behavior is unchanged).
6. If anything misbehaves, restore the backup with the June USB stick.

## Known risks / what only hardware can confirm

- The Project July tile is new. The APK passes `apktool b`, `baksmali` of the
  new dex, the resource gate and signature verification, but only the oven can
  prove Chef renders the tile and the Dialog opens with a valid window token.
- The oven page assumes Chef's WebView reports a loopback `RemoteAddr`; the
  oven-side routes are loopback-only by design. If the page 403s or does not
  load, capture the oven page URL/RemoteAddr before restoring.
- Android 5 WebView layout of the version badge and the new Phone access card.
- `/data/june/settings.json` must be writable by the june-local service
  (`/data/june` is already used by identity/updater, so this should hold).
- The update test requires the `2026102502` release to be uploaded to
  downloads.project-july.org; the ISO alone cannot make the release server
  serve it.
- The DNS/roots code is unit-tested, but only the oven can prove `getprop
  net.dns1` and the embedded bundle reach the Cloudflare chain end to end.
- This is a **test build** (`0.9.2-test`), not the final 0.9.2 release.

## Build-provenance notes for the release owner

- `installer/liveiso/prepare_assets.sh` was fixed for the flat installer tree:
  - synthesize the empty `{}` `companions_state.json` that PR #12 removed but
    `deploy_onoven.sh` still stages;
  - generate `accounts_template.db` in `/tmp` and copy it in (SQLite cannot
    create/lock a DB on this SMB mount);
  - locate `check_apk_resources.py` in `installer/scripts/` as well as the old
    `$ROOT/scripts/` path;
  - prefer `Chef_cookmodes_updates_signed.apk`, then the clean
    `Chef_cookmodes_signed.apk`, and only then the withdrawn foodack build.
- The ISO was built from the staged assets above with
  `JUNE_VERSION=0.9.2-test`; the canonical `june-local-live.iso` and the
  versioned archive are in `installer/liveiso/`.
- Updater fix files: `golocal/netenv.go`, `golocal/roots.go`,
  `golocal/certs/roots.pem`, plus `updater.go`/`status.go`/`oven_page.go`.
  `onoven/deploy_onoven.sh` now finds the real Ventoy mount for the token/QR;
  `.dockerignore` keeps old ISOs out of the Docker context.
- `scripts/verify-release.sh` gates `golocal/certs/roots.pem` on GTS Root R4;
  the Go tests include `netenv_test.go` and `roots_test.go`.
- Signed release: `dist/oven-updates-0.9.2/stable/` (2026102502);
  client package: `dist/june-wireless-update/` (rebuilt Linux + Windows).
