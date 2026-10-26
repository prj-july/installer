# June Local 0.9.2: what to build

Build 0.9.2 from **prj-july/installer `main` after PR #16 is merged**. Check with
`git log -1` that the merge commit of #16 is in. Already on main: #13 (sections no longer
collapse on refresh, status built only when read) and #14 (Send report on /local).

PR #16 brings:
- the new /local look (installer fonts and tokens, tabs, an actionable Software tile, the 3-step update flow, "update password" wording)
- report preview (`GET /local/report/preview`), an include-logs choice and an email fallback to reports@
- "Go back to the previous app" (`POST /local/update/restore`), restoring the Chef backup the boot applier keeps
- "Install from a file" (`POST /local/update/upload`), taking a signed bundle; make one with `june-update-publisher bundle -repo DIR -out X.zip`
- an update password QR on the installer's last screen, also saved as `june-logs/update-password-qr.svg` on the stick

**Also for 0.9.2: a Chef patch that adds Settings → Project July updates on the oven screen.** See CHEF-UPDATES-SCREEN.md. The june-local side (`/local/oven`, one-time phone codes) is in #16.

No changes to june_reaper.sh, the manifest format, the signing key or the publish steps.
The ISO needs `qrencode` (already in liveiso/build.sh).

## Smoke test on a real oven (in addition to the usual boot/app check)
1. `https://<oven>/local` renders in the new look, fonts load, and tabs survive the 7 s refresh.
2. Scan the installer's Update password QR. /local opens on Updates with the password filled in, and the address bar shows only `#updates`.
3. Check now works. If a newer signed release is published, Download and get ready, then restart, applies it.
4. After that apply, "Go back to the previous app" shows. Go back, restart, and the previous Chef is running; Check now then reports up to date for the undone release.
5. Report a problem: "See exactly what will be sent" shows no tokens or serial, and Send gives a JR- code.

## Release notes draft (Highlights)
- /local has a new look that matches the June USB installer and the app.
- Report a problem shows exactly what it sends, lets you leave out log lines, and can prepare an email instead.
- Updates: an update password QR code, Go back to the previous app, and Install from a file for ovens without internet.
