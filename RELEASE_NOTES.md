> **Withdrawn 2026-10-25 — do not install v0.9.0.** The Chef app build this
> release bakes is missing 35 resource files (its resource table names paths
> that are not in the APK), so an installed oven loops on boot. Stay on v0.7
> until the replacement build is published. Root cause, evidence and recovery:
> [`docs/incidents/2026-10-25-v0.9.0-chef-apk.md`](docs/incidents/2026-10-25-v0.9.0-chef-apk.md).

# June Local installer v0.9.0 release notes

Released: 2026-10-24

Build: `20261024-2321-92e76458`

ISO: `june-local-live.iso`

SHA-256: `1db073270cadf6d8cd9f2b49480882ccd3dea2212f28ae8055f1698314ccbe98`

Size: `1,416,314,880` bytes

v0.9.0 is v0.8.1 plus signed wireless updates for the oven. Everything in
v0.8.1 and v0.8 below still applies.

## Highlights

### Wireless updates, signed end to end

The oven can now pull a signed Chef APK release over outbound HTTPS — no
inbound ADB, port forwarding, or shared private key. The release manifest is
signed with an offline Ed25519 key, the public key is baked into the oven by
the installer, and updates are verified (size, SHA-256, signature) before
they are staged and atomically committed. Updates apply at boot with a
byte-for-byte backup and rollback, and a `min_sequence` baseline keeps an
older release from being replayed after a factory reset. Check and stage run
from the signed status page (Software updates) or the new
`june-wireless-update` client; controls require HTTPS and a per-install
control token, and the token is redacted from request logs.

This release bakes the Chef food-presence-acknowledgment build
(`Chef_cookmodes_foodack_signed.apk`, release `2026.10.24-chef-foodack`,
sequence `2026102401`) into the payload, matching the updater's signed
baseline. The release signing key never ships in the ISO.

Thank you, Phunctional, for the signed updater — publisher, clients, boot
apply, rollback, and the end-to-end tests. This is the largest community
contribution the installer has received.

## Known notes

- The updater publishes to
  `https://downloads.project-july.org/oven-updates/stable/`; ovens show an
  update error until the manifest is uploaded there.
- After a factory reset, run Check for update again: the reset restores the
  installer-baked Chef build and the updater baseline still prevents
  downgrades.

---

# June Local installer v0.8.1 release notes

Released: 2026-10-25

Build: `20261024-1938-6a424294`

ISO: `june-local-live.iso`

SHA-256: `8bb37bc681c61ba5d45ead47d46645f8795a3640db4b03b8a33533115a8e035e`

Size: `1,413,529,600` bytes

v0.8.1 is v0.8 plus two community contributions from Matt (@mvanhorn) and two
installer screen fixes. Everything in v0.8 below still applies.

## Highlights

### Cook status for Home Assistant and phones

The oven now serves its live cook status on its own, without the full
`/local/status` blob:

- `http://<oven-ip>/local/cook` returns JSON with the cavity and probe
  temperature, the target and mode, the time remaining and the door state.
- Open it in a browser (or `/local/cook.html`) for a live page.
- A cook that has finished shows as the previous cook, not as a live one.

### Restores check the backup first

Restore used to check only an image's size, so a damaged backup could still be
written back to the oven. Now:

- Every restore checks the size and the `.sha256` file next to the image, and
  refuses an image that doesn't match. A restore does a dry run first.
- It refuses a destination that is the wrong size or is this computer's own
  disk.
- `latest` picks the newest backup that still verifies, so a rollback skips a
  damaged file.

Thank you, Matt, for both of these, with tests and demos included.

### Installer screen fixes

- Home screen: the Connect by name tile's text no longer runs into its icon.
- Success screen: the CA certificate card's text no longer spills into the
  next card.

## Do I need to update?

- If your oven runs v0.8 and works, updating is optional. Re-install to get the
  cook status page.
- On v0.7 or older, don't factory reset: install v0.8.1 first. If a reset
  already left the oven "disconnected", installing v0.8.1 recovers it; then
  pair again.
- Back up the oven before you install anything.

---

# June Local installer v0.8 release notes

Released: 2026-10-24

Build: `20261024-1602-b5ca5293`

ISO: `june-local-live.iso`

SHA-256: `19378a2b9d75339d82655c8cf032b2d5caead5da71968563a5d94f7a45f824f3`

Size: `1,413,529,600` bytes

## Highlights

### Factory reset is no longer fatal

The oven now ships a per-oven identity seed, and June Local re-creates the
June account row on the next boot when a factory reset wiped it. A reset no
longer leaves the oven permanently "disconnected"; re-pair the phone or Home
Assistant afterwards and the oven is back. The status page also reports the
identity state directly, so this failure mode is visible instead of mysterious.

This fix exists because of a detailed community report: a correctly installed
v0.7 oven still showed `oven.connection=disconnected` after a factory reset,
and the reporter patiently captured the symptoms, tested the workaround, and
re-tested the fix. Thank you, Brooks—your report turned a day-long mystery
into a design we could actually ship.

### Two clear install modes

- **Connect by IP** (recommended): a per-home certificate authority and a
  certificate that includes the oven's LAN IP. No hostname or DNS setup.
- **Connect by name (june.local)**: the project CA goes into the oven's trust
  store, and the new **JuneDns** helper advertises `june.local`,
  `june2.local`, or `june3.local` over mDNS. If multicast is blocked on the
  network, add a local DNS entry or use Connect by IP.

### Every paired device stays paired

The oven now serves the complete paired-companion roster, so pairing a second
phone no longer hides Home Assistant or earlier phones after a reboot.

### Status page tells the truth

The `/local` status page now shows the oven identity, certificate trust state,
and clear red/yellow problems with concrete fix instructions—including the
exact factory-reset recovery path.

## Known notes

- mDNS is multicast: some networks (guest/isolated VLANs, aggressive IGMP
  snooping) block it. **Connect by IP** always works.
- In both modes the phone must trust the CA: download it from
  `http://<oven-ip>/local/ca.crt` after the install.
- Keep the project CA private key offline. A basic install makes it the trust
  anchor for every oven that uses it; Connect by IP uses a unique per-home CA
  instead.
- Make a full backup from the installer before changing anything; the
  installer can restore it.

Thanks to everyone who reported and reproduced the beta issues. This release
is yours as much as ours.
