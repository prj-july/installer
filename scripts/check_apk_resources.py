#!/usr/bin/env python3
"""Fail a release build when an APK's resource table names files it lacks.

Why this exists
---------------
The APK that shipped as the v0.9.0 Chef patch (sha256 be5ff47e...) was rebuilt
in an environment with a case-insensitive filesystem.  June's stock Chef APK
contains resource files whose names differ only by case (`res/Rq.xml` next to
`res/RQ.xml`, `res/Ee.png` next to `res/ee.png`, ...; Android/zip are
case-sensitive).  The rebuild silently collapsed each pair, so
`resources.arsc` still named 35 paths that were no longer inside the zip.  The
installer swapped that APK into `/system/priv-app/Chef/Chef.apk` before Android
started, Chef could not load those resources, the kiosk app crash-looped, and
every installed oven appeared to boot-loop.

That is a four-line check away: every `res/...` path named by
`resources.arsc` must exist in the zip at exactly that case.  Run this over the
APKs a release is about to bake and publish (the ISO payload's
`Chef_cookmodes_signed.apk` and friends).

Usage
-----
    python3 check_apk_resources.py APK [APK ...]

Exit status 0 when every APK is internally consistent, 1 otherwise.  Standard
library only: the build host does not need apktool/aapt.
"""

from __future__ import annotations

import hashlib
import os
import re
import sys
import zipfile

# resources.arsc keeps the path of every file-backed resource (layout XML,
# drawable PNG, raw asset, ...) in its global string pool.  The charset stops at
# the pool entry's length prefix / the next entry, which is enough to recover
# the path.  Entries not matched here can only make the check more permissive.
PATH_RE = re.compile(rb"res/[A-Za-z0-9_./\-]+\.[A-Za-z0-9]+")


def sha256_file(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(8 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def referenced_res_paths(arsc: bytes) -> set[str]:
    found = {match.decode("ascii", "replace") for match in PATH_RE.findall(arsc)}
    if not found:
        # Some tables encode the string pool as UTF-16; collapse the NUL bytes
        # (which may split each ASCII character) and retry.
        found = {
            match.decode("ascii", "replace")
            for match in PATH_RE.findall(arsc.replace(b"\x00", b""))
        }
    return found


def check_apk(path: str) -> int:
    """Return 0 if the APK is consistent, 1 if it is not."""
    name = os.path.basename(path)
    print("APK: %s (%d bytes, sha256 %s)" % (name, os.path.getsize(path), sha256_file(path)))
    try:
        apk = zipfile.ZipFile(path)
    except (OSError, zipfile.BadZipFile) as exc:
        print("FAIL %s: cannot open as a zip: %s" % (name, exc))
        return 1

    with apk:
        bad = apk.testzip()
        if bad is not None:
            print("FAIL %s: corrupt zip entry %r" % (name, bad))
            return 1
        names = apk.namelist()
        if "AndroidManifest.xml" not in names:
            print("FAIL %s: no AndroidManifest.xml" % name)
            return 1
        if not any(entry.endswith(".dex") for entry in names):
            print("FAIL %s: no classes.dex/classes*.dex" % name)
            return 1
        try:
            arsc = apk.read("resources.arsc")
        except KeyError:
            print("NOTE %s: no resources.arsc, nothing to cross-check" % name)
            return 0

        entry_names = set(names)
        res_entries = {entry for entry in entry_names if entry.startswith("res/")}
        referenced = referenced_res_paths(arsc)
        missing = sorted(ref for ref in referenced if ref not in entry_names)

        print(
            "  zip entries: %d  res/ entries: %d  resources.arsc paths: %d  missing: %d"
            % (len(names), len(res_entries), len(referenced), len(missing))
        )
        if not missing:
            return 0

        # Point at the case-variant that swallowed the file: that is the
        # signature of a rebuild done on a case-insensitive filesystem.
        by_lower: dict[str, list[str]] = {}
        for entry in res_entries:
            by_lower.setdefault(entry.lower(), []).append(entry)
        print("FAIL %s: resources.arsc names %d resource files the APK does not contain:"
              % (name, len(missing)))
        for ref in missing:
            variants = by_lower.get(ref.lower())
            hint = (" (only present as %s)" % ", ".join(sorted(variants))) if variants else ""
            print("  missing: %s%s" % (ref, hint))
        print(
            "  This APK will crash the app that loads these resources. Rebuild the APK on a "
            "case-sensitive filesystem and re-run this check before baking/publishing it."
        )
        return 1


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print(__doc__.strip().splitlines()[0], file=sys.stderr)
        print("usage: %s APK [APK ...]" % os.path.basename(argv[0]), file=sys.stderr)
        return 2
    status = 0
    for apk in argv[1:]:
        status |= check_apk(apk)
    if status:
        print("APK resource integrity check FAILED")
    else:
        print("APK resource integrity check passed")
    return status


if __name__ == "__main__":
    sys.exit(main(sys.argv))
