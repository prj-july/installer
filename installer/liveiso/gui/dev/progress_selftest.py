#!/usr/bin/env python3
"""Exercise server.py's dd progress/step logic without an oven (dev only).

Reproduces the v0.9.0 beta complaint: the worker writes the whole image, dd
reports 100%, and the GUI then showed "WRITE OVEN STORAGE - 0 S LEFT" while
dd's conv=fsync (restore) or the SHA256 read-back (backup) kept working for
minutes.  The watcher must switch to the finishing phase there -- no ETA, just
a growing "finishing" count -- and must never render a 0-second estimate while
bytes remain.

Feeds synthetic dd output (the same record format the real workers and
dev/mock_worker.py produce) through server.LogWatcher, including a final 100%
record followed by silence, and checks the resulting states.

Run from the gui directory:   python3 dev/progress_selftest.py
Or from anywhere:            python3 installer/liveiso/gui/dev/progress_selftest.py

Excluded from the ISO: build.sh rsyncs gui/ with --exclude dev/.
"""

import os
import sys
import tempfile
from pathlib import Path

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import server  # noqa: E402  (the path insert above finds the real server.py)

TOTAL = server.IMAGE_BYTES
MIB = 1024 * 1024


def check(name, got, want):
    if got != want:
        raise SystemExit(f"FAIL {name}: got {got!r}, want {want!r}")
    print(f"ok   {name}: {got!r}")


def check_true(name, cond, detail=""):
    if not cond:
        raise SystemExit(f"FAIL {name}: {detail}")
    print(f"ok   {name}{(': ' + detail) if detail else ''}")


def record(watcher, done, rate=10.0, secs=10):
    """Append one dd progress record, as dd rewrites it with a bare carriage return."""
    gb = done / 1e9
    gib = done / 2**30
    text = f"\r{done} bytes ({gb:.1f} GB, {gib:.1f} GiB) copied, {secs} s, {rate:.1f} MB/s "
    with open(watcher.path, "ab") as fh:
        fh.write(text.encode())
    watcher.poll()


def feed(watcher, text):
    with open(watcher.path, "ab") as fh:
        fh.write(text.encode())
    watcher.poll()


def step_key(watcher, action):
    """The tracker step the GUI would show for this log (Job.step_index logic)."""
    steps = server.ACTION_STEPS[action]
    idx = 0
    for i, key in enumerate(steps):
        if key in watcher.steps_seen:
            idx = i
    return steps[idx]


def main():
    fd, path = tempfile.mkstemp(prefix="june-progress-selftest-", suffix=".log")
    os.close(fd)
    try:
        watcher = server.LogWatcher(Path(path))

        # --- restore preamble: image check, then the write starts -------------
        feed(watcher, "[*] Looking for June backup images on USB...\n")
        feed(watcher, "[*] Verifying backup image before writing...\n")
        feed(watcher, "[+] Backup image OK: 261012-1200.img (7818182656 bytes)\n")
        check_true("no progress before dd", watcher.progress() is None)
        check("step before dd is the verify step", step_key(watcher, "restore"), "verify")

        feed(watcher, "[*] Restoring 261012-1200.img -> /dev/sdb\n")
        check("step during write", step_key(watcher, "restore"), "write")
        write_pat = next(pat for key, pat in server.STEP_PATTERNS if key == "write")
        check_true("write step tracks the restoring line",
                   write_pat.match("[*] Restoring a.img -> /dev/sdb") is not None)

        # --- writing: honest percentage and ETA -------------------------------
        record(watcher, TOTAL // 4)
        p = watcher.progress()
        check("phase while writing", p["phase"], "writing")
        check("pct while writing", p["pct"], 25.0)
        check("finishing counter while writing", p["finishing"], 0)
        check_true("eta while writing is a positive number of seconds",
                   p["eta"] is not None and p["eta"] > 0, f"eta={p['eta']}")

        record(watcher, TOTAL // 2)
        check("pct at half", watcher.progress()["pct"], 50.0)

        # --- the last sliver: ETA must clamp to 1 s, never 0 ------------------
        record(watcher, TOTAL - 4 * MIB, rate=10.0)
        p = watcher.progress()
        check("pct on the last block is below 100", p["pct"], 99.9)
        check("eta on the last block never floors to 0", p["eta"], 1)

        # --- dd reports the whole image, then goes quiet ----------------------
        record(watcher, TOTAL)
        p = watcher.progress()
        check("phase at 100%", p["phase"], "finishing")
        check("pct at 100%", p["pct"], 100.0)
        check("bytes at 100%", p["bytes"], TOTAL)
        check_true("no ETA in the finishing phase (this was the 0 S bug)", p["eta"] is None)
        check("finishing starts at 0", p["finishing"], 0)

        # --- silence while conv=fsync drains the write cache ------------------
        watcher.image_done_at -= 240  # pretend the flush has been going for 4 min
        p = watcher.progress()
        check("still finishing after 4 min of silence", p["phase"], "finishing")
        check_true("finishing clock advances", p["finishing"] >= 240, f"finishing={p['finishing']}s")
        check_true("no ETA after 4 min of silence", p["eta"] is None)

        # The records dd prints around the summary ("records in/out") and the
        # worker's closing lines must not reset the phase either.
        feed(watcher, "\n1864+1 records in\n1864+1 records out\n")
        feed(watcher, f"{TOTAL} bytes (7.8 GB, 7.3 GiB) copied, 751.7 s, 10.4 MB/s\n")
        feed(watcher, "[+] Restore complete.\n")
        p = watcher.progress()
        check("phase survives the closing lines", p["phase"], "finishing")
        check_true("phase is sticky until the job exits", p["eta"] is None)

        # --- backup shape: dd finishes, then SHA256 reads the image back ------
        backup_steps = server.ACTION_STEPS["backup"]
        check_true("backup has a verify step after the copy",
                   backup_steps.index("verify") > backup_steps.index("copy"))
        check_true("restore label is unchanged", server.STEP_LABELS["write"], "Write oven storage")

        print("\nPASS: restore/backup progress ends in an honest finishing state, not '0 s left'.")
        return 0
    finally:
        try:
            os.unlink(path)
        except OSError:
            pass


if __name__ == "__main__":
    sys.exit(main())
