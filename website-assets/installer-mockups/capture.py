#!/usr/bin/env python3
"""Capture faithful June Local installer mockups from the real GUI mock mode.

The script starts the installer backend in --mock mode, drives the actual
HTML/CSS/JS with headless Chromium, and writes PNGs plus a gallery and caption
file into this folder.  Nothing here is an AI-generated approximation: these
are renders of the real installer screens.

Setup (once):
    python3 -m venv .venv
    .venv/bin/pip install playwright
    .venv/bin/playwright install chromium

Run:
    .venv/bin/python capture.py
"""

from __future__ import annotations

import os
import socket
import subprocess
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parent.parent                      # installer repo root
LIVEISO = REPO / "installer" / "liveiso"
SERVER = LIVEISO / "gui" / "server.py"
PNG = HERE / "png"
PORT = int(os.environ.get("MOCK_PORT", "8765"))
BASE = f"http://127.0.0.1:{PORT}"
VERSION = os.environ.get("JUNE_VERSION", "0.8.1")
BUILD_ID = os.environ.get("JUNE_BUILD_ID", "20261024-1938-6a424294")
# Re-render for a release with JUNE_VERSION=<version> JUNE_BUILD_ID=<build>, e.g.
# JUNE_VERSION=0.8.1 JUNE_BUILD_ID=20261024-1938-6a424294 .venv/bin/python capture.py

STAGES = [
    ("01-home.png", "Installer home",
     "June Local installer home. The header shows the version and build; the tiles are Install June Local, Install with your own CA, Back up, Restore and More."),
    ("02-basic-options.png", "Basic install options",
     "Basic install: the oven name (default june) and the persistence option. The project CA is added to the oven's trust store and the factory messaging app stays in place."),
    ("03-basic-persistence.png", "Basic install with persistence",
     "Persistence selected: 'Keep June Local after a factory reset' stages the cook-modes, camera, light and messaging patches in /system so a factory reset keeps them."),
    ("04-custom-options.png", "Custom install options",
     "Custom install with a per-home CA: the oven LAN IP and hostname. The hostname defaults to june.local."),
    ("05-connect-waiting.png", "Connect the oven (waiting)",
     "Steps on the left, the USB/SW2 illustration on the right, and the oven-found box below. Waiting for RCM mode."),
    ("06-connect-found.png", "Oven found, 3 s auto-start",
     "The oven is detected; the box counts down from 3 seconds and starts the install automatically. Back cancels the countdown."),
    ("07-installing.png", "Installing",
     "Progress, elapsed time and the step tracker while the installer runs. The fan note explains that the fans spin up and stay fast."),
    ("08-success.png", "Install success + QR codes",
     "Success screen with the restart/shutdown buttons and QR codes for the oven status page, setup guide, CA certificate and companion app (phone access on)."),
    ("09-backup-destination.png", "Backup destination",
     "Choose the backup drive, folder and file name. The current boot USB is listed and marked as such."),
    ("10-restore-confirm.png", "Restore confirmation",
     "Restore: choose the backup image and type RESTORE to confirm. The warning explains that the oven is overwritten."),
    ("11-phone-sheet.png", "Phone access",
     "Connect a phone: the QR code opens the same installer on the phone so jobs can be started and followed from there."),
]


def wait_port(port: int, timeout: float = 20.0) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        with socket.socket() as s:
            s.settimeout(0.4)
            if s.connect_ex(("127.0.0.1", port)) == 0:
                return True
        time.sleep(0.2)
    return False


def start_backend() -> subprocess.Popen | None:
    if wait_port(PORT, 0.5):
        print(f"[*] using the backend already listening on {BASE}")
        return None
    payload = Path("/tmp/june-mock-payload")
    payload.mkdir(parents=True, exist_ok=True)
    (payload / "VERSION").write_text(VERSION + "\n", encoding="utf-8")
    (payload / "BUILD_ID").write_text(BUILD_ID + "\n", encoding="utf-8")
    env = dict(os.environ, MOCK_SPEED=os.environ.get("MOCK_SPEED", "0.35"))
    log = open("/tmp/june-mock-server.log", "w", encoding="utf-8")
    proc = subprocess.Popen(
        [sys.executable, str(SERVER), "--mock", "--bind", "127.0.0.1",
         "--port", str(PORT), "--payload", str(payload)],
        env=env, stdout=log, stderr=subprocess.STDOUT,
    )
    if not wait_port(PORT, 25.0):
        proc.terminate()
        raise SystemExit("mock backend did not start (see /tmp/june-mock-server.log)")
    print(f"[*] mock backend on {BASE} (log: /tmp/june-mock-server.log)")
    return proc


def api(page, path: str, payload: dict) -> None:
    page.evaluate(
        """async ([p, b]) => {
            await fetch(p, {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(b)});
        }""",
        [path, payload],
    )


def shot(page, name: str, full: bool = False) -> None:
    PNG.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(PNG / name), full_page=full)
    print(f"[+] {name}")


def main() -> None:
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        raise SystemExit("Playwright is missing. Run: .venv/bin/pip install playwright && .venv/bin/playwright install chromium")

    proc = start_backend()
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True, args=["--no-sandbox"])
            context = browser.new_context(
                viewport={"width": 1600, "height": 900},
                device_scale_factor=1.2,
                color_scheme="dark",
            )
            page = context.new_page()
            page.goto(BASE, wait_until="domcontentloaded")
            page.wait_for_selector("#main .view", timeout=20000)
            page.add_style_tag(content="#dev{display:none!important} #conn{display:none!important}")
            page.wait_for_function(
                "(v) => document.querySelector('#brand-sub')"
                " && document.querySelector('#brand-sub').textContent.includes(v)",
                arg=VERSION,
                timeout=10000,
            )
            page.wait_for_timeout(700)

            shot(page, "01-home.png")

            page.click('[data-act="install"]')
            page.wait_for_selector("#f-name", timeout=10000)
            page.wait_for_timeout(300)
            shot(page, "02-basic-options.png")

            page.check('input[name="persist"]', force=True)
            page.wait_for_timeout(300)
            shot(page, "03-basic-persistence.png")

            page.click('[data-do="home"]')
            page.wait_for_selector('[data-act="custom"]', timeout=10000)
            page.click('[data-act="custom"]')
            page.wait_for_selector("#f-ip", timeout=10000)
            page.wait_for_timeout(300)
            shot(page, "04-custom-options.png")

            page.fill("#f-ip", "192.168.1.100")
            page.click("#next")
            page.wait_for_selector(".connect", timeout=10000)
            page.wait_for_timeout(400)
            shot(page, "05-connect-waiting.png")

            api(page, "/api/mock/oven", {"present": True})
            page.wait_for_selector(".detect.found", timeout=10000)
            page.wait_for_timeout(150)
            shot(page, "06-connect-found.png")

            # The 3 s countdown starts the custom install automatically.
            page.wait_for_selector(".run", timeout=15000)
            page.wait_for_timeout(2500)
            shot(page, "07-installing.png")

            api(page, "/api/remote", {"enabled": True})
            page.wait_for_selector("text=June Local is installed", timeout=90000)
            page.wait_for_selector(".dl-card img", timeout=15000)
            page.wait_for_timeout(500)
            shot(page, "08-success.png", full=True)

            page.click('[data-do="dismiss"]')
            page.wait_for_selector('[data-act="backup"]', timeout=10000)
            page.click('[data-act="backup"]')
            page.wait_for_selector('input[name="dest"]', timeout=15000)
            page.wait_for_timeout(300)
            shot(page, "09-backup-destination.png")

            page.click('[data-do="home"]')
            page.wait_for_selector('[data-act="restore"]', timeout=10000)
            page.click('[data-act="restore"]')
            page.wait_for_selector('input[name="dest"]', timeout=15000)
            page.click('input[name="dest"]', force=True)
            page.click('[data-do="find"]')
            page.wait_for_selector('input[name="image"]', timeout=15000)
            page.click('input[name="image"]', force=True)
            page.fill("#f-confirm", "RESTORE")
            page.wait_for_timeout(300)
            shot(page, "10-restore-confirm.png")

            page.click('[data-do="home"]')
            page.wait_for_selector("#phone-btn", timeout=10000)
            page.click("#phone-btn")
            page.wait_for_selector("#pair-qr img", timeout=15000)
            page.wait_for_timeout(500)
            shot(page, "11-phone-sheet.png")

            browser.close()
    finally:
        if proc is not None:
            proc.terminate()

    captions = ["# June Local installer mockups", "",
                f"Rendered from the real installer UI (version {VERSION}, build {BUILD_ID}).", "",
                "| File | Stage | What it shows |", "| --- | --- | --- |"]
    for name, title, caption in STAGES:
        captions.append(f"| `png/{name}` | {title} | {caption} |")
    captions.append("")
    (HERE / "captions.md").write_text("\n".join(captions), encoding="utf-8")

    cards = "\n".join(
        f'<figure><img src="png/{name}" alt="{title}"><figcaption><b>{title}</b><br>{caption}</figcaption></figure>'
        for name, title, caption in STAGES
    )
    (HERE / "index.html").write_text(
        """<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>June Local installer mockups</title>
<style>body{margin:0;background:#08090b;color:#f4f4f5;font:16px/1.5 system-ui,sans-serif}
main{max-width:1100px;margin:0 auto;padding:32px 20px 80px}h1{font-size:30px}p{color:#a1a1aa}
figure{margin:28px 0;background:#14171c;border:1px solid #2a2e35;border-radius:14px;padding:14px}
img{width:100%;height:auto;border-radius:10px;display:block}figcaption{padding:12px 4px 2px;color:#a1a1aa}
b{color:#f4f4f5}</style></head><body><main><h1>June Local installer mockups</h1>
<p>Faithful renders of the real installer UI. Regenerate with <code>capture.py</code>.</p>""" + cards + "</main></body></html>\n",
        encoding="utf-8",
    )
    print(f"[+] wrote {HERE / 'captions.md'} and {HERE / 'index.html'}")


if __name__ == "__main__":
    main()
