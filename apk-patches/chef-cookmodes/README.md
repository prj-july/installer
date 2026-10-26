# Chef patch: cook modes (`plan_index`) + Project July tile

The stock oven Chef app refuses every remote `11002` primitive start except
bake, roast, airfry, pizzaiolo, sous_vide and grill
(`CookParamValidator.isValidFoodToStartRemotely`), and it has no way to pick a
plan for multi-plan primitives. Remote toast therefore either fails today or,
after only widening the whitelist, would always start plan 0 (level 1).

Chef also had no way into june-local's oven-side Updates page. The oven's real
Settings screen is a separate app (`JuneSettings.apk`), so a Chef-only patch
cannot add a row inside it. Instead, `patch.py` adds a **"Project July" tile to
Chef's own home-screen launcher** that opens the existing full-screen WebView
dialog on `http://127.0.0.1/local/oven`.

`patch.py` changes four things in a decoded Chef APK:

1. `CookParamValidator.isValidFoodToStartRemotely` allows every primitive in
   `CookPrograms.PRIMITIVE_PROGRAM_MAP`, except `blow_dry` (cleaning cycle).
2. `StartCookCommandData` gains an optional Jackson field `plan_index`
   (`@JsonProperty("plan_index")`, default `-1`).
3. `RemoteFoodLoadListener` selects `food.selectPlan(plan_index)` before the
   stock run-state and recipe checks; an out-of-range index is answered
   not-allowed instead of starting the wrong plan.
4. The "Project July" launcher tile:
   - `LauncherListBuilder.projectJuly()` builds one `LauncherCell$Icon` with
     mode id `0x30` (new; the existing launcher mode ids top out at `0x25`),
     icon `launcher_chef` (`0x7f0800a5`) and the literal label
     `"Project July"`.
   - `LauncherListSource$Dragon.fill()` and `LauncherListSource$Meerkat.fill()`
     call it immediately after `settings()`, so the tile sits on the home
     screen next to Settings.
   - `LauncherActivity.onIconClicked(...)` intercepts `p1 == 0x30` at method
     entry and calls `OvenUpdatesDialog.show(p0)`. `p0` is the
     `LauncherActivity`, so the Dialog gets a valid window token.

The label is a smali string constant, not a resource, because the build keeps
`resources.arsc` binary (`apktool d -r`). The `OvenUpdatesDialog` helper classes
live in `smali/com/projectjuly/chef/` and are copied into a fresh
`smali_classes3` (its own dex), so the primary dex's method count does not
matter. A Dialog needs no manifest entry or layout, which is why this works
without touching the binary manifest.

The old six primitives and every client that omits `plan_index` behave exactly
as before (`plan_index < 0` keeps the first plan).

## Wire contract

```
11002 {"primitive_type":"toast","temperature_cavity":260000,"plan_index":4}
```

Toast level N maps to `plan_index = N-1` (0..8), `temperature_cavity = 260000`,
fixed durations 150000 + index*30000 ms. Other multi-plan modes:

| primitive | plan_index | temperature_cavity |
|---|---|---|
| grill | 0 high, 1 medium, 2 low | 232222 / 204444 / 135000 |
| reheat | 0 timed, 1 probe | 176667 |
| slow_cook | 0 high, 1 low | 160000 / 140000 |

Single-plan modes: bake 176667 (user value allowed), roast 204444, airfry
204444, pizzaiolo 260000, sous_vide 60000, proof 29444, warm 76667, dehydrate
57222, broil 260000. Broil intensity is a separate `11010 SetBroilIntensity`
command and needs the running plan id from `10014`.

## Build

```
./build_patched.sh [stock-Chef.apk] [output.apk]
```

On a copy that cannot hold the execute bit (for example this SMB share, mounted
with `nounix`), run it as `bash build_patched.sh ...`.

The default stock APK is the healthy-build extraction at
`local-resources/june-rcm/healthy-build/decompiled/priv-app-apks/Chef.apk`.
The default output is `Chef_cookmodes_updates_signed.apk` next to this README.
The script decodes with resources kept raw (`apktool d -r`), patches, rebuilds
and signs with `tools/certs/june_apk_sign.p12`.

Decode, rebuild and signing all happen in a temp dir under `/tmp`; only the
final signed APK is copied back. **Never build the decoded tree on a
case-insensitive filesystem**: the v0.9.0 APK was rebuilt that way, lost the 35
resource files whose names differ only by case, and boot-looped the ovens.

The signed APK is intentionally not committed: it contains June's original
assets and is ~246 MB. Keep the built APK next to this recipe (or in the
installer working tree) for ISO builds; the public installer ships the recipe
and builds the APK from the user's own extracted stock Chef APK.

Current build:

```
Chef_cookmodes_updates_signed.apk
246,891,601 bytes
sha256 2062834c1a4b26aa4093946a42188e666ee5a127539b8df91a902feb424b5b8e
```

The clean 0.9.1 cook-modes-only build is still present as
`Chef_cookmodes_signed.apk` (sha256 `8a006b80…`) for fallback. The foodack build
is withdrawn; do not bake or deploy `Chef_cookmodes_foodack_signed.apk`.

Build history: 64edf1e4 and 6a1b2bc9 had inverted smali branches and were
rejected live by Chef (`invalid food name` for bake). c3825ab0 fixed both
branches and passed bake/toast/reheat/proof/warm/dehydrate/broil live, but
remote `slow_cook` rebooted the oven because its plans carry 0-duration
placeholder steps until the UI sets a duration. 8a006b80 blocks `slow_cook`
cleanly (`not-allowed`) and is the clean 0.9.1 cook-modes build. 2062834c adds
the Project July home-screen tile (mode 0x30) on top of 8a006b80: built on a
case-sensitive `/tmp` filesystem, `AndroidManifest.xml` and `resources.arsc`
byte-identical to 8a006b80, only dex content differs, and
`check_apk_resources.py` reports 614/614 resource paths present. Not yet tested
on an oven.

## Deploy

Chef is a priv-app, so it cannot be `pm install -r`'d over the June build.
Stage the APK and let `june_reaper.sh` swap it into the collector volume:

* persistent (survives factory reset): `/system/etc/june/chef_cookmodes.apk`
* one-shot: `/data/june/chef_cookmodes.apk` (removed after it is applied)

The live target inside the collector volume is
`/system/priv-app/Chef/Chef.apk`. The ISO payload keeps the canonical
`Chef_cookmodes_signed.apk` name that `install.sh`/`deploy_onoven.sh` expect;
`installer/liveiso/prepare_assets.sh` prefers
`Chef_cookmodes_updates_signed.apk` when it is present. The installer
integration adds the staging copy plus the `apply_staged_apk` block in
`june_reaper.sh.junelocal`; this recipe only builds the APK.
