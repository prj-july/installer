# june-backup

Checks a June oven backup before anyone writes it back, and dry-runs a restore.
Nothing is written unless you pass `-write` and `-confirm RESTORE`.

The live installer already writes a `.sha256` sidecar after a backup. Restore
used to check only the file size. This tool (and `backup.sh`) now refuse a
restore unless the sidecar is present and the digest matches.

Works on a laptop with the backup file; it does not need the oven.

## Commands

```sh
cd installer/backup
go test ./...
go build -o june-backup ./cmd/june-backup

./june-backup checksum -image /path/to/261012-1200.img
./june-backup verify   -image /path/to/261012-1200.img
./june-backup latest   -dir /media/JUNE/june-backups
./june-backup restore  -image /path/to/261012-1200.img          # dry-run
./june-backup restore  -dir /media/JUNE/june-backups -device /dev/sdb
./june-backup restore  -image /path/to/261012-1200.img \
    -device /dev/sdb -write -confirm RESTORE                   # writes
```

`-expected-size` defaults to `7818182656` (the June eMMC). Tests and demos
pass a smaller size.

A restore stops, in plain language, when:

- the file is the wrong size
- there is no `.sha256` sidecar
- the digest does not match (damaged or incomplete file)
- `-device` is this computer's disk, or the wrong size
- `-write` is set without `-confirm RESTORE`

`-dir` picks the newest image that still verifies, so a rollback uses the last
known-good backup and skips damaged files.

The sidecar stores only the basename (`HASH  261012-1200.img`), so it still
checks after the USB is remounted at a different path. Sidecars that still
have the old absolute path are accepted too.

## Live installer

`prepare_assets.sh` builds `june-backup` into the ISO payload when Go is on
the host. `backup.sh verify` and the restore path call it; if the binary is
missing they fall back to `sha256sum` with the same refusals. Restore still
uses `dd status=progress` so the graphical tracker keeps working.

```sh
backup.sh verify /media/JUNE/june-backups/261012-1200.img
backup.sh restore-dry /media/JUNE/june-backups/261012-1200.img
```
