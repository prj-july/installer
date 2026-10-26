# Chef patch for 0.9.2: "Project July updates" on the oven screen

FnG wants this in 0.9.2, tested on his oven before anything ships. The june-local side is
done in prj-july/installer PR #16 (branch `terablo/local-redesign`). The Chef side needs the
decoded stock Chef, which only this machine has, so it's yours.

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

So Chef only needs a way in: **one Settings entry that opens a full-screen WebView on
`http://127.0.0.1/local/oven`.** All the UI and logic live in june-local.

## The patch

Extend `apk-patches/chef-cookmodes/patch.py` so the existing build produces one APK with both
the cook-modes changes and this one. Give it its own idempotent marker, for example
`June Local: updates screen`.

1. **New activity**, written in smali by hand: `com/projectjuly/chef/OvenUpdatesActivity extends android/app/Activity`.
   - `onCreate`: request no title and fullscreen to match Chef's other screens (copy the window flags Chef's Settings activity uses).
   - Create a `WebView`. Turn on JavaScript and DOM storage. Set a `WebViewClient` whose `shouldOverrideUrlLoading` keeps only `http://127.0.0.1/` URLs in the view and refuses everything else.
   - Set it as the content view, then `loadUrl("http://127.0.0.1/local/oven")`.
   - `onBackPressed`: `finish()`.
   - `onDestroy`: `webView.destroy()`.
2. **AndroidManifest.xml:** register the activity with `exported="false"`, the same `screenOrientation` and theme as Chef's Settings activity. Chef must already have INTERNET; check it.
3. **Settings entry:** find Chef's Settings / About screen and add a row labelled
   "Project July updates" that runs `startActivity(new Intent(this, OvenUpdatesActivity.class))`.
   The build decodes with `apktool d -r` (resources kept raw), so **you can't add new layout or
   string resources**. Build the row in code, or reuse an existing row view and set its text in
   smali. If adding a row is fragile, a long-press on the About/version row is acceptable. Write
   down which way you chose in the README.
4. **Safety:**
   - Run `scripts/check_apk_resources.py` on the output.
   - Confirm that `apktool d` of the result shows the new class and the manifest entry.
   - Confirm that cook modes still pass the existing live checks.
   - Remember 0.9.0: the APK must never be rebuilt on the P: share or any case-insensitive filesystem. Build in `/tmp` or `~`.

## Testing on FnG's oven before release

The installed oven applies `/system/etc/june/chef_cookmodes.apk`. The one-shot `/data` copy is
ignored while that file exists, so test it like this:

1. Take a June USB backup first.
2. Build a test ISO from `terablo/local-redesign` with the new Chef baked in, and install it.
   That gives the same june-local and Chef that 0.9.2 will ship.
3. On the oven, open Settings → Project July updates:
   - The screen loads, shows the installed release, and Check works.
   - The QR opens `/local` on a phone, signed in. Typing the code on /local works too, and the
     code works only once.
   - Back returns to Settings.
   - A normal cook still starts, and a remote toast level still works.
4. If anything misbehaves, restore the backup with the June USB stick.

## Then

- Update `apk-patches/chef-cookmodes/README.md`: what the patch does, the new APK sha256 and the build history line.
- Bake the new APK into 0.9.2 and bump `min_sequence` per `WIRELESS_UPDATES.md` if you also publish it as a signed wireless release.
- The smoke test in `BUILD-NOTES.md` still applies, plus step 3 above.
