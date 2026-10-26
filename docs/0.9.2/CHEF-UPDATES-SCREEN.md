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

Most of it is already in this branch:

- `apk-patches/chef-cookmodes/smali/com/projectjuly/chef/` holds `OvenUpdatesDialog` and its
  inner classes, written by hand in smali:
  - `show(Context)` opens a full-screen `Dialog` with a `WebView` on
    `http://127.0.0.1/local/oven`, with JS and DOM storage on and no cache.
  - `LocalOnly` keeps navigation on `http://127.0.0.1/` and refuses everything else.
  - `Cleanup` destroys the WebView on dismiss.
  - `Open` is an OnClickListener and OnLongClickListener that calls `show(view.getContext())`.
  - `attach(View)` sets the long-press listener on a view.
  - All of it runs under a `catch Throwable` that only logs, so it can't crash Chef.
- It's a Dialog, not a new Activity, because `build_patched.sh` decodes with `apktool d -r`.
  That keeps the manifest and resources binary, so there's no manifest entry or layout to add.
- `patch.py` has a new `add_updates_screen()` step. It copies those classes into a fresh
  `smali_classesN`, its own dex, so the primary dex's method count doesn't matter on ART.
  Then it applies `SETTINGS_HOOK`.

**What's left for you: fill in `SETTINGS_HOOK` in `patch.py`.** It's the one Chef-specific
spot, a triple:
1. the smali file in the decoded tree
2. an anchor that occurs exactly once in that file
3. the line inserted after the anchor

Pick whatever is most robust in Chef's Settings/About screen. Either of these works:

- `invoke-static {vX}, Lcom/projectjuly/chef/OvenUpdatesDialog;->attach(Landroid/view/View;)V`
  on an existing row view, which gives you a long-press. This is the least intrusive option.
- `invoke-static {p0}, Lcom/projectjuly/chef/OvenUpdatesDialog;->show(Landroid/content/Context;)V`
  from an existing click handler, if there's a row that can be repurposed.
  `p0` must be the Activity, or a context whose window token is the Activity's.

If you can add a real "Project July updates" row with a code-built TextView and an
`OvenUpdatesDialog$Open` click listener, even better, but it's optional for 0.9.2. Note in the
README which entry you chose, so FnG knows where to find it (for example, long-press "About").

I couldn't assemble the smali here. Please check that `apktool b` builds it cleanly, and that
`baksmali` of the new dex shows the four classes.

**Safety:**
- Run `scripts/check_apk_resources.py` on the output.
- Confirm that cook modes still pass the existing live checks.
- Remember 0.9.0: never rebuild on the P: share or any case-insensitive filesystem. Build in `/tmp` or `~`.

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
