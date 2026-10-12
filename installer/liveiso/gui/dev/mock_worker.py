#!/usr/bin/env python3
"""Stand-in for install.sh / backup.sh when server.py runs with --mock.

Prints the same stage lines and dd progress records as the real workers, on
a shortened timeline, so the UI can be developed without an oven attached.
Any argument containing "fail" makes the run fail at the RCM step.
MOCK_SPEED=<factor> speeds the timeline up (default 1).
"""

import sys
import time

SPEED = float(__import__("os").environ.get("MOCK_SPEED", "1"))
IMAGE = 7818182656
FAIL = __import__("os").environ.get("MOCK_FAIL") == "1"


def say(line: str, delay: float = 0.6) -> None:
    print(line, flush=True)
    time.sleep(delay / SPEED)


def connect(fail: bool) -> None:
    say("[*] Installer dev, build: dev")
    say("[*] Looking for an existing June UMS disk (up to 15s)...", 1.5)
    say("[*] Waiting for June APX device (0955:7740). Put the oven in RCM now (SW2 + power).", 2.5)
    say("[*] Oven is not in UMS mode. Loading U-Boot with tegrarcm...")
    say("[*] APX devices before tegrarcm:")
    say("Bus 003 Device 012: ID 0955:7740 NVidia Corp.")
    say("[*] tegrarcm attempt 1/3 (45s USB timeout)...", 2)
    if fail:
        say("tegrarcm: read uid: Resource temporarily unavailable", 0.3)
        say("[!] tegrarcm attempt 1 failed (exit 1).", 0.3)
        say("[-] The oven stopped answering in RCM mode (tegrarcm: Resource temporarily unavailable).", 0.1)
        say("[-] Unplug the oven power, wait 5 seconds, then hold SW2 and power it on again (keep holding ~10s), and retry.", 0.1)
        sys.exit(1)
    say("uid: 0x640010017410b1c4000000001c0180c0", 0.4)
    say("RCM version: 40.1", 0.4)
    say("downloading miniloader to target at address 0x4000e000 (136756 bytes)...", 1)
    say("miniloader downloaded successfully", 0.6)
    say("sending file: u-boot-dtb-tegra.bin", 1.5)
    say("u-boot-dtb-tegra.bin sent successfully", 0.5)
    say("[*] Waiting for the June eMMC block device (up to 60s)...", 2.5)
    say("[*] target disk: /dev/sdb (excluded boot/system disk: /dev/sda)")


def dd(label: str, seconds: float = 24) -> None:
    start = time.time()
    rate = 10.4
    while True:
        t = (time.time() - start) * SPEED
        done = min(IMAGE, int(IMAGE * t / seconds))
        sys.stdout.write(f"\r{done} bytes ({done/1e9:.1f} GB, {done/2**30:.1f} GiB) copied, {int(t*30)} s, {rate:.1f} MB/s ")
        sys.stdout.flush()
        if done >= IMAGE:
            break
        time.sleep(0.5 / SPEED)
    sys.stdout.write("\n1864+1 records in\n1864+1 records out\n")
    sys.stdout.write(f"{IMAGE} bytes (7.8 GB, 7.3 GiB) copied, 751.7 s, {rate} MB/s\n")
    sys.stdout.flush()


def install(args: list[str]) -> None:
    mode = args[0] if args else "prepackaged"
    fail = FAIL or any("fail" in a for a in args)
    if mode == "generate":
        say("Generating per-home CA (RSA 2048)...", 1.2)
        say("Signing leaf certificate for " + (args[1] if len(args) > 1 else "?"), 0.8)
        say("[*] CA cert saved to /media/ventoy/june-certs/ca.crt")
    say(f"[debug] MODE={mode}", 0.1)
    connect(fail)
    say("[*] Install log: /tmp/june-install-20261003-120000.log")
    say("[*] binary: /opt/june-installer/june-local-linux-armv7", 0.2)
    say("[*] cert:   /opt/june-installer/junelife_chain_godaddy_intermediate.crt", 0.2)
    say("[*] flashing boot image to LNX (seek=76544)", 2)
    say("[*] flashing boot image to SLN (seek=2755328)", 2)
    say("[+] boot image flashed to LNX + SLN (8388608 bytes)", 1)
    say("[*] standard install keeps the factory JuneCloudMessaging app (no JCM patch staged)", 1.5)
    say("[+] deployed. SHA256 of staged files:", 0.5)
    say("[+] power-cycle the oven normally; the junelocal init service starts june-local at boot_completed.")
    say("==============================================================", 0.1)
    say("  SUCCESS: June Local is installed on the oven", 0.1)
    say("==============================================================", 0.1)


def backup(args: list[str]) -> None:
    mode = args[0] if args else "backup"
    fail = FAIL or any("fail" in a for a in args)
    connect(fail)
    if mode == "backup":
        say("[*] Preparing to back up the full June eMMC (7818182656 bytes) to USB.")
        say("[*] Writing backup image to: " + "/".join(a for a in args[1:4] if a))
        dd("backup")
        say("[*] Verifying backup with SHA256 (reads the whole image back; usually 1-3 minutes)...", 5)
        say("[+] Backup complete.")
        say("[+] SHA256: 3f9a0c1e5b7d2a4c6e8f0a1b3c5d7e9f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d")
    elif mode == "restore":
        confirm = sys.stdin.readline().strip()
        say("[*] Looking for June backup images on USB...")
        say("[*] Verifying backup image before writing...")
        say("[+] Backup image OK: " + (args[3] if len(args) > 3 else "oven.img") + " (7818182656 bytes)")
        say("[*] Dry-run restore (nothing will be written)")
        say("[+] Would write the image. Nothing was written.")
        say("[*] WARNING: this overwrites the oven eMMC with " + (args[3] if len(args) > 3 else "?"))
        if confirm != "RESTORE":
            say("[-] Aborted.")
            sys.exit(1)
        say("[*] Restoring " + (args[3] if len(args) > 3 else "?") + " -> /dev/sdb")
        dd("restore")
        say("[+] Restore complete.")


if __name__ == "__main__":
    worker, rest = sys.argv[1], sys.argv[2:]
    {"install.sh": install, "backup.sh": backup}[worker](rest)
