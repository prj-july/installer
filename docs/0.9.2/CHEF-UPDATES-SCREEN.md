# Chef patch for 0.9.2: "Project July updates" on the oven screen

FnG wants this in 0.9.2, tested on his oven before anything ships. The june-local side is
done in prj-july/installer PR #16 (branch `terablo/local-redesign`). The Chef side is
implemented in `apk-patches/chef-cookmodes/patch.py`; this file records the entry point and
how it was verified.

## What june-local now serves (PR #16)

These only answer requests from the oven itself, meaning a loopback `RemoteAddr`.

- `GET http://127.0.0.1/local/oven` is the whole Updates screen. It's ES5 HTML/JS for the old
  Android 5 WebView, with big touch buttons for Check, Install, Restart-to-finish and Go back,
  plus a QR code and an 8-character one-time code so a phone can open `https://<oven>/local`
  already signed in.
- `POST /local/oven/update/check|stage|restore` and `GET /local/oven/signin` are called by that
  page. They need no update password, because being at the oven is the authorization.
- june-local already listens on `0.0.0.0:80` (`june_bootclear.sh.onoven`), so
  `http://127.0.0.1/` works from Chef. Android 5 has no cleartext restriction.

## The entry point: a "Project July" home-screen tile

The original plan was one Settings entry. The oven's real Settings screen belongs to a
separate app (`JuneSettings.apk`), so a Chef-only patch cannot add a row inside it. Product
decision: add a **"Project July" tile to Chef's own launcher**, immediately after the Settings
tile, and open the existing full-screen WebView dialog from there.

- Mode id `0x30` (new; the existing launcher mode ids top out at `0x25`).
- Label `"Project July"` as a smali `const-string`; resources stay binary under
  `apktool d -r`, so no new string resource is possible.
- Icon `launcher_chef` (`0x7f0800a5`), using the same `DrawableImageResource` pattern as
  `settings()`. The fallback is `launcher_settings` (`0x7f0800b0`).
- `LauncherListBuilder.projectJuly()` mirrors `settings()` and uses `cells.size()` as the
  index. It is inserted just before `settings()`.
- `LauncherListSource$Dragon.fill()` and `LauncherListSource$Meerkat.fill()` call
  `projectJuly()` immediately after `settings()`, in the same chained-builder style.
- `LauncherActivity.onIconClicked(ILjava/lang/String;Ljava/lang/String;)V` checks
  `p1 == 0x30` at method entry and calls `OvenUpdatesDialog.show(p0)`. `p0` is the
  `LauncherActivity`, so the Dialog gets a valid window token. `v4` is reused because the
  stock code overwrites it immediately afterwards.

## The patch

Most of the Chef side is in `apk-patches/chef-cookmodes/`:

- `smali/com/projectjuly/chef/` holds `OvenUpdatesDialog` and its inner classes, written by
  hand in smali:
  - `show(Context)` opens a full-screen `Dialog` with a `WebView` on
    `http://127.0.0.1/local/oven`, with JS and DOM storage on and no cache.
  - `LocalOnly` keeps navigation on `http://127.0.0.1/` and refuses everything else.
  - `Cleanup` destroys the WebView on dismiss.
  - `Open` is an OnClickListener and OnLongClickListener that calls `show(view.getContext())`.
  - `attach(View)` sets the long-press listener on a view.
  - All of it runs under a `catch Throwable` that only logs, so it can't crash Chef.
- It's a Dialog, not a new Activity, because `build_patched.sh` decodes with `apktool d -r`.
  That keeps the manifest and resources binary, so there's no manifest entry or layout to add.
- `patch.py` has two relevant steps:
  - `add_updates_screen()` copies the helper classes into a fresh `smali_classesN`. Chef has
    `smali` and `smali_classes2`, so this is `smali_classes3`, its own dex.
  - `patch_launcher_tile()` adds `projectJuly()`, both `fill()` calls and the `onIconClicked`
    check. Each injected region carries `# June Local: updates screen`; the function skips
    already-patched files, and raises `SystemExit` if any anchor does not match exactly once.

## Build and verification (done)

```
./build_patched.sh
# on a share mounted without the execute bit:
bash build_patched.sh
```

The build is done entirely inside `/tmp` on a case-sensitive filesystem, and only the final
signed APK is copied back. Output:

```
Chef_cookmodes_updates_signed.apk
246,891,601 bytes
sha256 2062834c1a4b26aa4093946a42188e666ee5a127539b8df91a902feb424b5b8e
```

Checks against that APK:

- `apktool b` succeeded. Re-decoding the result shows `classes3.dex` and all four
  `com/projectjuly/chef/OvenUpdatesDialog*` classes.
- The built dex contains `LauncherListBuilder.projectJuly()`, the `projectJuly()` calls in
  both `LauncherListSource$Dragon.fill()` and `$Meerkat.fill()`, and the
  `const/16 v4, 0x30` / `if-ne p1, v4` check in `onIconClicked`.
- `python3 scripts/check_apk_resources.py` reports 1610 zip entries, 614 `res/` entries,
  614 `resources.arsc` paths, 0 missing.
- Compared with the clean 0.9.1 cook-modes build (`Chef_cookmodes_signed.apk`, sha256
  `8a006b80…`): `AndroidManifest.xml` and `resources.arsc` are byte-identical, all 614
  `res/` entries and all `assets/` entries are identical. The only payload differences are
  `classes.dex` and the added `classes3.dex` (plus the `META-INF` signature files).
- `jarsigner -verify` passes with the project's self-signed `CN=June Local APK` certificate,
  the same certificate as `8a006b80…`.

## Testing on FnG's oven before release

The installed oven applies `/system/etc/june/chef_cookmodes.apk`. The one-shot `/data` copy is
ignored while that file exists, so test it like this:

1. Take a June USB backup first.
2. Build a test ISO from `terablo/local-redesign` with the new Chef baked in, and install it.
   That gives the same june-local and Chef that 0.9.2 will ship.
3. On the oven, tap **Project July** on the home screen (just after the Settings tile):
   - The screen loads, shows the installed release, and Check works.
   - The QR opens `/local` on a phone, signed in. Typing the code on /local works too, and the
     code works only once.
   - Back returns to the launcher home screen.
   - A normal cook still starts, and a remote toast level still works.
   - Layout: one extra cell can push the last tile (Scale) onto another page or row. Confirm
     the tile is visible and reachable and that nothing overlaps.
4. If anything misbehaves, restore the backup with the June USB stick.

## Then

- `apk-patches/chef-cookmodes/README.md` is updated with the tile, the new APK sha256/size and
  the build-history line.
- Bake the new APK into 0.9.2 and bump `min_sequence` per `WIRELESS_UPDATES.md` if you also
  publish it as a signed wireless release.
- The smoke test in `BUILD-NOTES.md` still applies, plus step 3 above.
