# June Local wireless updates

June Local can pull a signed Chef APK update over outbound HTTPS. This does not
require inbound ADB, port forwarding, a shared private key, or a custom cloud.
The oven verifies the release manifest before downloading anything, then verifies
the APK size and SHA-256 before staging it under `/data`.

This first release deliberately supports app/content updates only. Bootloader,
boot image, system image, kernel and full-firmware updates need partition-aware
power-loss recovery and a separate approval path.

## Security model

- `manifest.json` is signed as exact bytes by an offline Ed25519 release key.
- `manifest.sig` is the base64 signature of those exact bytes.
- The public key is baked into `/system/etc/june/updater.json` by the installer.
- `min_sequence` records the signed release baseline baked into both installer
  system slots, preventing replay below that release after a factory reset.
- The manifest names a monotonic release sequence, channel, target, artifact
  size, SHA-256, Android package and version code.
- The updater accepts only `june-oven-android5` and the `chef` artifact.
- Artifact paths are relative and cannot escape the signed repository path.
- Downloads are written to a temporary file, fsynced, hash-checked, ZIP-checked,
  and written into an isolated `/data/june/updates/transactions/txn-*` directory.
- The APK, metadata and SHA-256 marker are fsynced inside that directory before
  one atomic `/data/june/updates/ready` marker commits the transaction.
- Update check/stage requests require HTTPS and a random per-install control
  token. The token and common credential headers are redacted from request logs.
- The release private key never ships in the ISO, oven image, installer assets,
  web page, or updater client.

The per-install token is authorization, not a release signing key. If one oven's
token leaks, it can only attempt to stage a valid signed release on that oven.

## Repository build

The publisher and client use only the Go standard library.

```sh
cd golocal
make test
make tools
make client-windows
make client-linux
../scripts/package-wireless-client.sh
```

`make tools` creates:

```text
golocal/bin/june-update-publisher
golocal/bin/june-wireless-update
golocal/bin/june-wireless-update-windows-amd64.exe
golocal/bin/june-wireless-update-linux-amd64
```

The project release key is intentionally excluded from Git:

```text
.secrets/update-signing/release-2026.private.pem
.secrets/update-signing/release-2026.public.txt
```

Back up the private key offline and keep file mode `0600`. Losing it means future
ovens need a new public key installed by the USB installer. Publishing the
private key permits anyone to sign updates for every installed oven.

Generate a new key pair only when rotating release trust:

```sh
golocal/bin/june-update-publisher keygen \
  -private .secrets/update-signing/release-2027.private.pem \
  -public .secrets/update-signing/release-2027.public.txt
```

## Publish a release

Use the same filename in the command below as the artifact the boot applier
expects. The publisher copies it into `dist/oven-updates/stable/artifacts/` and
creates a signed manifest beside it.

```sh
golocal/bin/june-update-publisher publish \
  -private-key .secrets/update-signing/release-2026.private.pem \
  -out dist/oven-updates/stable \
  -channel stable \
  -release 2026.10.24-chef-foodack \
  -sequence 2026102401 \
  -notes-file dist/notes-2026.10.24.txt \
  -artifact chef=Chef_cookmodes_foodack_signed.apk
```

The resulting layout is:

```text
dist/oven-updates/stable/
  manifest.json
  manifest.sig
  artifacts/Chef_cookmodes_foodack_signed.apk
```

Upload the whole `stable/` directory to:

```text
https://downloads.project-july.org/oven-updates/stable/
```

Every published release sequence must be greater than the previous release.
Ovens reject an equal or older sequence and never downgrade from a newer signed
release.

When an installer ISO bakes a newly published Chef APK into its payload, bump
`min_sequence` in `onoven/updater.json` to that release's sequence before
building the ISO.

## Installer integration

`onoven/updater.json` contains the public key, stable channel, signed repository
URLs and target. Both the normal and factory-fallback system partitions receive
that file at `/system/etc/june/updater.json`.

During installation, `deploy_onoven.sh` creates a random per-install token at:

```text
/data/june/updates/control-token
```

It also writes the token to `/run/june-installer/update-token` and, when the
Ventoy boot partition is mounted, to:

```text
/june-logs/update-token.txt
```

An existing token is preserved during reinstall. The installer log prints it
once when generated.

## Update an oven

Open the signed status page over HTTPS:

```text
https://<oven-ip>/local
```

If the per-home CA is not trusted by the browser, install the CA from
`/local/ca.crt` first. HTTP status remains readable, but check/stage controls
are disabled there so the control token never crosses the LAN in plaintext.

In **Software updates**, enter the control token, choose **Check for update**,
then **Stage for next boot**. Once staged, power-cycle the oven normally.

The command-line client is an alternative:

```sh
golocal/bin/june-wireless-update \
  -oven https://192.168.1.100 \
  -ca june-local-ca.crt \
  -token-file update-token.txt status

golocal/bin/june-wireless-update \
  -oven https://192.168.1.100 \
  -ca june-local-ca.crt \
  -token-file update-token.txt check

golocal/bin/june-wireless-update \
  -oven https://192.168.1.100 \
  -ca june-local-ca.crt \
  -token-file update-token.txt stage
```

For scripted staging, add `-yes`. `JUNE_UPDATE_TOKEN_FILE` and
`JUNE_UPDATE_TOKEN` are also accepted by the client.

## Boot apply and rollback

Before Android starts, the patched `june_reaper.sh` finds the updater transaction,
validates its SHA-256 marker, and mounts the encrypted collector read-write. It:

1. backs up the live Chef APK to `/data/june/chef_stock_backup.apk`;
2. copies and compares the staged APK;
3. restores the backup if the copy or comparison fails;
4. verifies the previous live APK was backed up byte-for-byte before replacing it;
5. promotes the verified APK to `/system/etc/june/chef_cookmodes.apk` in the
   active system slot;
6. records release metadata in `/data/june/updates/applied.json` and
   `/system/etc/june/chef_update.json`, so downgrade protection survives a
   `/data` reset;
7. atomically clears the ready marker before removing the committed transaction.

Logs are written to:

```text
/data/june/logs/chef_patch.log
/data/june/logs/updater.log
```

If the update download is interrupted, no ready marker is written. If boot apply
fails, the stock APK is retained or restored and the committed transaction stays
in place for inspection or retry. Corrupt release metadata disables the updater
instead of silently resetting downgrade protection.

The active system copy survives normal reboots. A factory reset restores the
installer-baked APP image from the SAP fallback slot, so the wireless-updated
Chef build is not retained across that reset. The fallback `updater.json`
contains `min_sequence`, which prevents replaying anything older than the
installer baseline. Check for updates again after a factory reset.
