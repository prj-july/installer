# Publishing the June Local installer

Everything that *builds* the installer lives in this folder, but the payload
inputs (June APKs, certificates, firmware blobs, prebuilt binaries) currently
come from sibling folders and must not all be committed to a public repo.

## What is in this folder

| Path | Kind | Publish |
|------|------|---------|
| `build.sh` | ISO build + the `june-menu` installer UI | yes |
| `build_live_iso.sh` | Docker wrapper, stamps `BUILD_ID` | yes |
| `prepare_assets.sh` | Generates `assets/` (embeds `install.sh` and `backup.sh`) | yes |
| `Dockerfile` | Debian live-build environment | yes |
| `flash_to_ventoy.sh` | Copies the ISO to a Ventoy stick | yes |
| `README.md`, `TEST_RUNBOOK.md`, `PUBLISHING.md` | docs | yes |
| `assets/` | Generated payload (binaries, APKs, certs) | no (regenerated) |
| `vm-record*/` | Local VM test recordings | no |

## Paths

`prepare_assets.sh` derives `LR` (the `local-resources` checkout) from its own
location and `ROOT` as its parent. Override with `JUNE_LR` / `JUNE_ROOT` when
the published tree has a different layout:

```sh
JUNE_LR=/path/to/local-resources JUNE_ROOT=/path/to/June ./prepare_assets.sh
```

## Payload inputs and where they come from today

| Input | Default source | Publish |
|-------|----------------|---------|
| `deploy_onoven.sh`, `june_bootclear.sh.onoven`, `june_reaper.sh.junelocal` | `onoven/` | yes |
| `updater.json` (release public key/config) | `onoven/` | yes |
| `generate_certs.sh`, `sign_leaf.sh` | `packaging/` | yes |
| `install.sh`, `backup.sh` | embedded in `prepare_assets.sh` | yes |
| `june-local`, `june-camerad` armv7 binaries | `golocal/bin/` | prefer publishing `golocal/` source |
| `june-wireless-update` live-ISO client | `golocal/bin/` | yes; built from `cmd/june-wireless-update` |
| `companions_state.json` | repo root (supplied blob) | yes |
| `boot_junelocal_init.img` | `tools/orchestrator/` | no; publish `build_boot_junelocal_init.py` + `jcm_apply.sh` and build it |
| `JuneCloudMessaging.apk` | `june-rcm/healthy-build/…` | **no** (June proprietary) |
| `JuneCloudMessaging_nopin_signed.apk` | `apk-patches/jcm-pinning/` | **no** (modified June app) |
| `companion_endpoint_signed.apk` | `apk-patches/companion-endpoint/` | **no** |
| `junelife_chain_godaddy_intermediate.crt`, `june-local.key` | `June/tools/certs/` | **no** — contains June's private key |
| `tegrarcm`, `jetson-tk1.bct`, `u-boot-dtb-tegra.bin` | `june-rcm/` | no (June/NVIDIA firmware) |
| `upstream-content/` (presets/recipes mirror, ~287 MB) | `upstream-content/` (or `JUNE_CONTENT_DIR`) | **no** — June/third-party content, reference only |

The preset/recipe mirror is what makes guided programs (Proof, Slow Cook,
recipes) work: june-local serves `POST /1/presets/catalog` and
`GET /1/presets/bundle/...` from it. `prepare_assets.sh` includes it when
`JUNE_CONTENT_DIR` (default `upstream-content/`, falling back to the development
tree's `june-arch/capture/upstream-content`) exists, so
personal builds get working guided programs; published builds should omit it
for licensing reasons and document that users must supply their own content.

## Suggested published layout

```
june-installer/
  liveiso/        # this folder
  onoven/         # deploy/boot scripts and updater.json public trust config
  packaging/      # generate_certs.sh, sign_leaf.sh
  bootimage/      # build_boot_junelocal_init.py, jcm_apply.sh
  golocal/        # june-local / june-camerad and wireless updater sources
  blobs/          # NOT committed: June APKs, certs/key, firmware blobs (fetched or supplied locally)
```

With that layout, set `JUNE_LR` to the published root and provide `blobs/`
locally (or download it from a restricted release) before running
`prepare_assets.sh`.

## Releasing an ISO

The download filename is always `june-local-live.iso`, so the Ventoy setup
(`ventoy.json` pins `/june-local-live.iso`) never changes.  The version lives
in the download URL and inside the ISO: boot menu entries, the GUI header,
the text menu, and every install/backup log.

`build_live_iso.sh` also keeps a versioned local copy,
`june-local-live-<version>-<build-id>.iso` (with a matching `.sha256`), so
old builds stay reviewable for bug reports even though the canonical
`june-local-live.iso` is overwritten by each build. Both are gitignored;
published R2 objects under `installer/<version>/` are the canonical record.

1. Tag the release commit: `git tag v0.8 && git push origin v0.8`.
   `build_live_iso.sh` stamps `git describe` into `assets/VERSION`, so the
   tagged commit builds as `v0.8`; anything else builds as e.g.
   `v0.8-3-g9a0350c` (`-dirty` with uncommitted changes) and is clearly not a
   release.  `JUNE_VERSION=...` overrides it.
2. `./prepare_assets.sh && ./build_live_iso.sh`, then boot-test the ISO.
3. `sha256sum june-local-live.iso`.
4. Upload to R2 as `installer/v0.8/june-local-live.iso`.  Never overwrite a
   published version; old versions stay downloadable.
5. Point the `https://downloads.project-july.org/installer/latest` redirect
   (302) at the new object.
6. Upload `installer/latest.json`, which the June USB app
   (`installer/usbtool`) reads to download and verify the ISO:

   ```json
   {"version": "v0.8", "url": "https://downloads.project-july.org/installer/v0.8/june-local-live.iso",
    "sha256": "<sha256 from step 3>", "size": <bytes>}
   ```

   Serve it with a short cache lifetime (e.g. `Cache-Control: max-age=300`).

   `installer/releases.json` lists every published version the same way, plus
   its publish date, newest first and one version per line:

   ```json
   {"releases": [
   {"version": "v0.8", "url": "…/installer/v0.8/june-local-live.iso", "sha256": "<sha256>", "size": <bytes>, "date": "<publish date>"},
   {"version": "v0.7", "url": "…/installer/v0.7/june-local-live.iso", "sha256": "<sha256>", "size": <bytes>, "date": "2026-10-11"}
   ]}
   ```

   Each version also has `installer/VERSION/june-local-live.iso.sha256`.

`./publish_release.sh v0.8 june-local-live.iso` does steps 3, 4 and 6 in one go
with the `r2` rclone remote: it hashes the ISO, uploads it with its `.sha256`
(refusing to overwrite a published version), checks the public URL serves the
same size, then writes `latest.json` and adds the version to `releases.json`.
`--manifest-only` writes just the two manifests for an ISO that is already
uploaded; `--dry-run` shows what it would do. Step 5 (the redirect rule) stays
manual.
7. Update the SHA256 on the website and in `CREATE_VENTOY_USB.md`.

Builds from 0.6 on also carry `/boot/grub/june-bios.img` (legacy
BIOS boot for June USB sticks, from `make_bios_boot.sh`) and
`/june-version.txt`.
