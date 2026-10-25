#!/usr/bin/env python3
"""Generate the June Local website asset pack (SVG + PNG).

The SVGs are the editable source. PNGs are rendered with rsvg-convert.
All UI screens here are *mockups* built from the real installer copy/colors;
replace them with real screenshots when the tutorial photos are available.
"""

from __future__ import annotations

import html
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
SVG_DIR = ROOT / "svg"
PNG_DIR = ROOT / "png"

W, H = 1600, 900
BG = "#08090b"
BG2 = "#0f1115"
PANEL = "#14171c"
PANEL2 = "#1a1e24"
FG = "#f4f4f5"
MUTED = "#a1a1aa"
LINE = "#2a2e35"
EMBER = "#ff7a2e"
EMBER2 = "#ffb04a"
GREEN = "#4ade80"
AMBER = "#fbbf24"
BLUE = "#7cc4f2"
RED = "#e05252"
SANS = "DejaVu Sans, sans-serif"
MONO = "DejaVu Sans Mono, monospace"


def esc(s: object) -> str:
    return html.escape(str(s), quote=True)


def svg_doc(body: str, w: int = W, h: int = H) -> str:
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" '
        f'width="{w}" height="{h}">'
        f'<rect width="{w}" height="{h}" fill="{BG}"/>'
        f"{body}</svg>\n"
    )


def rect(x, y, w, h, fill=PANEL, stroke=LINE, rx=16, sw=1.5, extra=""):
    return (
        f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}" '
        f'fill="{fill}" stroke="{stroke}" stroke-width="{sw}" {extra}/>'
    )


def text(x, y, s, size=22, fill=FG, weight="400", font=SANS, anchor="start", extra=""):
    return (
        f'<text x="{x}" y="{y}" font-family="{font}" font-size="{size}" '
        f'font-weight="{weight}" fill="{fill}" text-anchor="{anchor}" {extra}>{esc(s)}</text>'
    )


def wrap_lines(s: str, width: int) -> list[str]:
    words = str(s).split()
    lines: list[str] = []
    cur = ""
    for word in words:
        candidate = f"{cur} {word}".strip()
        if cur and len(candidate) > width:
            lines.append(cur)
            cur = word
        else:
            cur = candidate
    if cur:
        lines.append(cur)
    return lines or [""]


def paragraph(x, y, s, width=42, size=20, fill=MUTED, lh=28, weight="400", font=SANS):
    out = []
    for i, line in enumerate(wrap_lines(s, width)):
        out.append(text(x, y + i * lh, line, size=size, fill=fill, weight=weight, font=font))
    return "".join(out)


def bullet(x, y, s, width=40, size=20, fill=MUTED, color=EMBER):
    return f'<circle cx="{x}" cy="{y - 6}" r="5" fill="{color}"/>' + paragraph(
        x + 20, y, s, width=width, size=size, fill=fill
    )


def pill(x, y, s, fill=EMBER, fg="#160a04", size=14, pad=14):
    w = int(len(s) * size * 0.62 + pad * 2)
    return (
        rect(x, y, w, size + 14, fill=fill, stroke=fill, rx=(size + 14) / 2, sw=0)
        + text(x + w / 2, y + size + 4, s, size=size, fill=fg, weight="700", anchor="middle")
    )


def card(x, y, w, h, title=None, body=None, accent=EMBER, title_size=28, pad=26):
    out = [rect(x, y, w, h, fill=PANEL)]
    out.append(f'<rect x="{x}" y="{y}" width="7" height="{h}" rx="3.5" fill="{accent}"/>')
    ty = y + pad + title_size * 0.8
    if title:
        out.append(text(x + pad, ty, title, size=title_size, weight="700", fill=FG))
    if body:
        out.append(paragraph(x + pad, ty + 40, body, width=int((w - 2 * pad) / 10.5), size=19))
    return "".join(out)


def arrow(x1, y1, x2, y2, color=EMBER, sw=4):
    # simple straight arrow; horizontal or vertical
    out = [f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{color}" stroke-width="{sw}" stroke-linecap="round"/>']
    if abs(x2 - x1) >= abs(y2 - y1):
        d = 1 if x2 >= x1 else -1
        out.append(f'<path d="M {x2} {y2} l {-14*d} -9 M {x2} {y2} l {-14*d} 9" stroke="{color}" stroke-width="{sw}" fill="none" stroke-linecap="round" stroke-linejoin="round"/>')
    else:
        d = 1 if y2 >= y1 else -1
        out.append(f'<path d="M {x2} {y2} l -9 {-14*d} M {x2} {y2} l 9 {-14*d}" stroke="{color}" stroke-width="{sw}" fill="none" stroke-linecap="round" stroke-linejoin="round"/>')
    return "".join(out)


def window_chrome(x, y, w, title, accent=EMBER):
    out = [rect(x, y, w, 52, fill="#1d2128", rx=16)]
    out.append(f'<rect x="{x}" y="{y + 36}" width="{w}" height="16" fill="#1d2128"/>')
    for i, color in enumerate(["#ff5f57", "#febc2e", "#28c840"]):
        out.append(f'<circle cx="{x + 24 + i * 24}" cy="{y + 26}" r="7" fill="{color}"/>')
    out.append(text(x + w / 2, y + 34, title, size=20, weight="600", fill=FG, anchor="middle"))
    return "".join(out)


def terminal(x, y, w, h, lines):
    out = [rect(x, y, w, h, fill="#050607", rx=14)]
    yy = y + 34
    for line in lines:
        color = FG
        if line.startswith("$"):
            color = GREEN
        elif line.startswith("#"):
            color = MUTED
        out.append(text(x + 22, yy, line, size=19, fill=color, font=MONO))
        yy += 30
    return "".join(out)


def title_header(title, subtitle=None, mockup=False):
    out = [text(64, 86, title, size=46, weight="700", fill=FG)]
    if subtitle:
        out.append(text(64, 126, subtitle, size=21, fill=MUTED))
    if mockup:
        out.append(pill(W - 230, 48, "MOCKUP", fill="#2a2e35", fg=EMBER2, size=15))
    return "".join(out)


def footer(s):
    return text(64, H - 38, s, size=18, fill=MUTED)


def make_overview():
    out = [title_header("June Local on a Ventoy USB", "Download → create the stick → boot → install or back up")]
    steps = [
        ("1", "Download", "Ventoy from ventoy.net and the latest june-local-live.iso. Verify the SHA256.", BLUE),
        ("2", "Create the stick", "Install Ventoy to a 16 GB+ USB. MBR, keep the exFAT partition label Ventoy.", EMBER),
        ("3", "Copy the files", "june-local-live.iso at the root, plus ventoy/ventoy.json. Leave ~8 GB free.", EMBER2),
        ("4", "Boot the laptop", "Handle Secure Boot, pick the USB, then choose the graphical installer.", GREEN),
        ("5", "Install or back up", "The live system runs from RAM. Logs go to june-logs, certs to june-certs.", AMBER),
    ]
    y = 210
    for i, (n, title, body, color) in enumerate(steps):
        x = 64 + i * 300
        out.append(card(x, y, 270, 300, None, None, accent=color, title_size=25, pad=20))
        out.append(f'<circle cx="{x + 40}" cy="{y + 40}" r="24" fill="{color}"/>')
        out.append(text(x + 40, y + 48, n, size=24, weight="700", fill="#160a04", anchor="middle"))
        out.append(text(x + 80, y + 50, title, size=25, weight="700", fill=FG))
        out.append(paragraph(x + 24, y + 112, body, width=24, size=19, fill=MUTED, lh=27))
        if i < len(steps) - 1:
            out.append(arrow(x + 278, y + 150, x + 296, y + 150, color=color))
    out.append(
        card(
            64,
            570,
            1472,
            220,
            "The project-specific rules",
            "Keep the Ventoy label; never FAT32 (the oven backup is ~7.3 GB); leave ~8 GB free; "
            "do not create a Ventoy persistence partition; the two boot menus are expected.",
            accent=RED,
            title_size=26,
        )
    )
    out.append(footer("Project July · June Local installer"))
    return svg_doc("".join(out))


def make_windows():
    out = [title_header("Install Ventoy on Windows", "Ventoy2Disk.exe — MBR, keep the Ventoy label", mockup=True)]
    out.append(window_chrome(120, 170, 1360, "Ventoy2Disk"))
    out.append(rect(160, 250, 1280, 560, fill=PANEL, rx=14))

    out.append(text(200, 310, "Device", size=21, fill=MUTED))
    out.append(rect(200, 330, 1200, 56, fill=BG2, rx=10))
    out.append(text(224, 368, "USB 32 GB  (E:)   ·  removable", size=22, fill=FG))
    out.append(pill(1210, 338, "correct device", fill="#243229", fg=GREEN, size=14))

    out.append(text(200, 430, "Option  →  Partition Style", size=21, fill=MUTED))
    out.append(rect(200, 450, 360, 54, fill=BG2, rx=10))
    out.append(text(224, 488, "MBR   (recommended)", size=22, fill=FG))
    out.append(rect(580, 450, 360, 54, fill=BG2, rx=10))
    out.append(text(604, 488, "Secure Boot Support: off", size=22, fill=MUTED))
    out.append(rect(960, 450, 440, 54, fill=BG2, rx=10))
    out.append(text(984, 488, "Show all devices: off", size=22, fill=MUTED))

    out.append(rect(200, 540, 1200, 74, fill="#241417", stroke="#4a2226", rx=10))
    out.append(text(224, 588, "Installing erases everything on this USB drive. Back it up first.", size=22, fill="#ffb4b4"))

    out.append(rect(200, 650, 260, 62, fill=EMBER, rx=12))
    out.append(text(330, 690, "Install", size=24, weight="700", fill="#160a04", anchor="middle"))
    out.append(rect(480, 650, 260, 62, fill=BG2, rx=12))
    out.append(text(610, 690, "Update", size=24, fill=MUTED, anchor="middle"))

    out.append(card(920, 640, 480, 140, "Result", "VTOYEFI · 32 MB     +     Ventoy · exFAT", accent=GREEN, title_size=23, pad=20))
    out.append(footer("Do not format or rename the large partition; the June Local installer looks for the Ventoy label."))
    return svg_doc("".join(out))


def make_linux():
    out = [title_header("Install Ventoy on Linux", "Whole-disk install; never pick a partition", mockup=True)]
    out.append(terminal(120, 190, 1360, 430, [
        "$ sudo apt install dosfstools exfatprogs",
        "$ lsblk -o NAME,SIZE,MODEL,TRAN,MOUNTPOINTS",
        "  sda    32G  USB 3.0  usb   /media/user/Ventoy",
        "$ sudo sh Ventoy2Disk.sh -i /dev/sda        # MBR",
        "# add -s for Secure Boot support",
        "# use -g only if you specifically need GPT",
        "$ # the installer erases the USB and creates:",
        "#   /dev/sda1  VTOYEFI  32M  FAT",
        "#   /dev/sda2  Ventoy  29G   exFAT",
    ]))
    out.append(card(120, 660, 640, 160, "If mkfs.vfat is missing", "Install dosfstools and exfatprogs (or exfat-utils on older distros).", accent=AMBER, title_size=23, pad=20))
    out.append(card(800, 660, 680, 160, "Keep the label", "The default label is Ventoy. The installer finds the stick by that label.", accent=GREEN, title_size=23, pad=20))
    out.append(footer("Use the whole disk (/dev/sdX). If you point Ventoy at a partition, you can destroy the wrong filesystem."))
    return svg_doc("".join(out))


def make_usb_layout():
    out = [title_header("What is on the stick", "Two Ventoy partitions; the large one is the June Local data partition")]
    out.append(rect(120, 210, 1360, 180, fill=PANEL, rx=24))
    out.append(rect(150, 245, 300, 110, fill="#1d2128", rx=16))
    out.append(text(300, 290, "VTOYEFI", size=28, weight="700", fill=FG, anchor="middle"))
    out.append(text(300, 325, "32 MB · FAT · do not touch", size=18, fill=MUTED, anchor="middle"))
    out.append(rect(470, 235, 980, 130, fill="#1b2418", stroke="#31512c", rx=16))
    out.append(text(960, 282, "Ventoy", size=30, weight="700", fill=GREEN, anchor="middle"))
    out.append(text(960, 320, "exFAT · keep the label · visible in Windows/Linux", size=19, fill=MUTED, anchor="middle"))

    items = [
        ("june-local-live.iso", "≈ 1.4 GB · boot image", EMBER),
        ("ventoy/ventoy.json", "auto-select the ISO", EMBER2),
        ("june-logs/", "installer logs", BLUE),
        ("june-certs/", "per-home CA + leaf", GREEN),
        ("free space", "≥ 8 GB for one ~7.3 GB oven backup", AMBER),
    ]
    y = 460
    for i, (name, sub, color) in enumerate(items):
        x = 120 + (i % 3) * 470
        yy = y + (i // 3) * 150
        out.append(card(x, yy, 430, 120, name, sub, accent=color, title_size=23, pad=20))

    out.append(card(120, 730, 1360, 90, "Never FAT32", "FAT32 cannot store the ~7.3 GB oven backup. Use the Ventoy exFAT default or NTFS.", accent=RED, title_size=23, pad=20))
    out.append(footer("16 GB minimum · 32 GB recommended if the same stick will hold a backup"))
    return svg_doc("".join(out))


def make_copy_config():
    out = [title_header("Copy the ISO and the config", "Root of the Ventoy partition is the simplest layout", mockup=True)]
    out.append(window_chrome(100, 170, 700, "Ventoy (E:) — file manager"))
    out.append(rect(130, 250, 640, 500, fill=PANEL, rx=14))
    rows = [
        ("june-local-live.iso", "1.4 GB", EMBER),
        ("ventoy/", "folder", EMBER2),
        ("june-logs/", "created at install", BLUE),
        ("june-certs/", "created at install", GREEN),
    ]
    yy = 300
    for name, sub, color in rows:
        out.append(f'<circle cx="165" cy="{yy-6}" r="10" fill="{color}"/>')
        out.append(text(195, yy, name, size=24, fill=FG))
        out.append(text(560, yy, sub, size=18, fill=MUTED, anchor="end"))
        yy += 70

    out.append(rect(840, 170, 660, 580, fill="#050607", rx=14))
    out.append(text(870, 220, "ventoy/ventoy.json", size=24, weight="700", fill=FG, font=MONO))
    cfg = [
        "{",
        '  "control": [',
        '    { "VTOY_DEFAULT_MENU_MODE": "0" },',
        '    { "VTOY_FILT_DOT_UNDERSCORE_FILE": "1" },',
        '    { "VTOY_MENU_TIMEOUT": "3" },',
        '    { "VTOY_DEFAULT_IMAGE":',
        '      "/june-local-live.iso" }',
        "  ]",
        "}",
    ]
    for i, line in enumerate(cfg):
        out.append(text(870, 280 + i * 38, line, size=21, fill=EMBER2, font=MONO))
    out.append(paragraph(870, 700, "If the ISO lives in /ISO/, use /ISO/june-local-live.iso and optionally set VTOY_DEFAULT_SEARCH_ROOT.", width=52, size=18))
    out.append(footer("The default image is selected after 3 seconds; the June Local menu then appears."))
    return svg_doc("".join(out))


def make_boot_menus():
    out = [title_header("The two boot menus", "Ventoy first, June Local second — both are expected", mockup=True)]
    # Ventoy menu
    out.append(window_chrome(100, 170, 660, "Ventoy"))
    out.append(rect(130, 250, 600, 470, fill="#050607", rx=14))
    out.append(text(170, 320, "Ventoy", size=30, weight="700", fill=EMBER2))
    out.append(rect(160, 360, 540, 64, fill="#241a12", stroke=EMBER, rx=10))
    out.append(text(190, 402, ">  june-local-live.iso", size=24, fill=FG, font=MONO))
    out.append(text(190, 472, "   Boot from local disk", size=22, fill=MUTED, font=MONO))
    out.append(text(190, 532, "   Reboot", size=22, fill=MUTED, font=MONO))
    out.append(pill(560, 640, "3 s", fill="#2a2e35", fg=EMBER2, size=16))

    # June Local menu
    out.append(window_chrome(840, 170, 660, "June Local Installer"))
    out.append(rect(870, 250, 600, 470, fill="#0b0d10", rx=14))
    out.append(text(1170, 315, "Project July", size=24, weight="700", fill=EMBER, anchor="middle"))
    out.append(text(1170, 350, "June Local installer", size=19, fill=MUTED, anchor="middle"))
    out.append(rect(900, 390, 540, 64, fill="#241a12", stroke=EMBER, rx=10))
    out.append(text(930, 432, ">  Graphical installer (default)", size=22, fill=FG, font=MONO))
    out.append(rect(900, 470, 540, 64, fill="#15181d", stroke=LINE, rx=10))
    out.append(text(930, 512, "   Basic text UI", size=22, fill=MUTED, font=MONO))
    out.append(pill(1300, 640, "3 s", fill="#2a2e35", fg=EMBER2, size=16))
    out.append(footer("Choose the graphical installer unless you are debugging. The text UI is there as a fallback."))
    return svg_doc("".join(out))


def make_secure_boot():
    out = [title_header("Secure Boot", "Enroll Ventoy's key, or disable Secure Boot for the install", mockup=True)]
    out.append(card(100, 190, 430, 240, "1 · Is Secure Boot on?", "Check the firmware (BIOS/UEFI). If it is off, boot the USB normally.", accent=BLUE, title_size=25))
    out.append(card(590, 190, 430, 240, "2 · Ventoy key", "Install Ventoy with Secure Boot Support, or choose the key-enrollment path below.", accent=EMBER, title_size=25))
    out.append(card(1080, 190, 420, 240, "3 · Or disable it", "For a one-time installer laptop, disabling Secure Boot is the simplest path.", accent=AMBER, title_size=25))

    out.append(rect(100, 480, 1400, 250, fill="#050607", rx=14))
    out.append(text(140, 540, "MokManager on first boot", size=28, weight="700", fill=FG, font=MONO))
    steps = [
        "Enroll key from disk",
        "→  VTOYEFI",
        "→  ENROLL_THIS_KEY_IN_MOKMANAGER.cer",
        "→  Continue  →  Yes  →  Reboot",
    ]
    for i, s in enumerate(steps):
        out.append(text(140, 610 + i * 34, s, size=22, fill=EMBER2, font=MONO))
    out.append(pill(1340, 500, "MOCKUP", fill="#2a2e35", fg=EMBER2, size=15))
    out.append(footer("After MokManager enrollment the USB boots normally on Secure Boot machines. If you disabled Secure Boot, re-enable it after the install if needed."))
    return svg_doc("".join(out))


def make_backup_swap():
    out = [title_header("Backups and the live RAM system", "The boot USB can be swapped for a backup drive")]
    out.append(card(90, 200, 440, 300, "Live system", "The June Local installer runs from RAM (toram). After boot, the boot USB is no longer required.", accent=BLUE, title_size=26))
    out.append(card(580, 200, 440, 300, "Swap if needed", "Unplug the Ventoy stick and plug in a second USB for the oven backup. Wait for the installer to say it is safe.", accent=EMBER, title_size=26))
    out.append(card(1070, 200, 440, 300, "Use the same stick", "If the Ventoy stick has ≥ 8 GB free, the installer lists it as Ventoy (current boot USB) and can write the backup there.", accent=GREEN, title_size=26))

    out.append(rect(90, 560, 1420, 230, fill=PANEL, rx=16))
    out.append(text(130, 615, "Backup target list (example)", size=25, weight="700", fill=FG))
    rows = [
        ("Ventoy (current boot USB)", "exFAT · 12.4 GB free", GREEN),
        ("BackupUSB", "exFAT · 28.1 GB free", EMBER2),
    ]
    for i, (name, sub, color) in enumerate(rows):
        y = 665 + i * 60
        out.append(f'<circle cx="150" cy="{y-6}" r="9" fill="{color}"/>')
        out.append(text(180, y, name, size=23, fill=FG))
        out.append(text(1420, y, sub, size=20, fill=MUTED, anchor="end"))
    out.append(footer("One backup is ~7.3 GB and takes 15–30 minutes at typical USB speeds. Logs go to june-logs/; certs go to june-certs/."))
    return svg_doc("".join(out))


def make_installer_ui():
    out = [title_header("June Local installer UI", "Faithful mockup from the real GUI copy and color system", mockup=True)]
    out.append(rect(70, 160, 1460, 660, fill="#0b0d10", stroke=LINE, rx=20))
    out.append(f'<rect x="70" y="160" width="1460" height="70" rx="20" fill="#0f1115"/>')
    out.append(f'<rect x="70" y="200" width="1460" height="30" fill="#0f1115"/>')
    out.append(f'<circle cx="120" cy="195" r="18" fill="{EMBER}"/>')
    out.append(text(152, 188, "Project July", size=24, weight="700", fill=FG))
    out.append(text(152, 214, "June Local installer 0.8 · build 20261024-1602-b5ca5293", size=15, fill=MUTED))
    out.append(pill(1320, 178, "OVEN CONNECTED", fill="#243229", fg=GREEN, size=14))
    out.append(text(100, 300, "What would you like to do?", size=48, weight="300", fill=FG))

    tiles = [
        ("Connect by IP", "No hostname or DNS needed. A per-home CA and a certificate for the oven's IP.", "RECOMMENDED", EMBER),
        ("Connect by name", "Adds the project CA. JuneDns advertises june.local.", None, EMBER2),
        ("Back up the oven", "A full copy of the oven's storage on a USB drive. 15 to 30 minutes.", None, BLUE),
        ("Restore a backup", "Puts a backup image back on the oven.", None, GREEN),
    ]
    for i, (name, sub, tag, color) in enumerate(tiles):
        x = 100 + i * 360
        out.append(rect(x, 340, 330, 260, fill="#14171c", rx=18))
        out.append(f'<circle cx="{x+50}" cy="390" r="26" fill="{color}"/>')
        out.append(text(x + 90, 400, name, size=18, weight="700", fill=FG))
        out.append(paragraph(x + 24, 460, sub, width=36, size=17, fill=MUTED, lh=23))
        if tag:
            out.append(pill(x + 220, 360, tag, fill="#3a2416", fg=EMBER2, size=12))
    out.append(text(100, 680, "Each job asks you to put the oven in recovery mode first. Nothing changes until you press the final button.", size=19, fill=MUTED))
    out.append(rect(100, 720, 300, 56, fill=BG2, rx=12))
    out.append(text(250, 756, "Use your phone instead", size=20, fill=FG, anchor="middle"))
    out.append(footer("Real screenshots can also be taken from the phone UI: open the QR link while the installer is running."))
    return svg_doc("".join(out))


def make_troubleshooting():
    out = [title_header("Troubleshooting quick cards", "The problems users hit while making the stick")]
    items = [
        ("mkfs.vfat: command not found", "Linux: install dosfstools and exfatprogs (or exfat-utils), then rerun Ventoy2Disk.sh.", AMBER),
        ("Ventoy does not list the ISO", "Put the ISO on the large exFAT Ventoy partition, not the 32 MB VTOYEFI partition. Check the .iso extension and re-plug.", BLUE),
        ("Stick is not offered as a backup target", "Keep the Ventoy label, use exFAT/NTFS (not FAT32), leave free space, and make sure it is writable. Otherwise use a second USB.", GREEN),
        ("Secure Boot verification error", "Enroll Ventoy's key with MokManager or disable Secure Boot in the firmware.", EMBER),
        ("No bootable device", "Check the boot order, try the other USB entry (UEFI vs Legacy), and confirm both Ventoy partitions exist.", RED),
        ("Very slow backup", "Use a USB 3 port and stick. The oven eMMC reads at roughly 8–10 MB/s, so a full backup takes 15–30 minutes.", EMBER2),
    ]
    for i, (title, body, color) in enumerate(items):
        x = 90 + (i % 3) * 480
        y = 190 + (i // 3) * 280
        out.append(card(x, y, 440, 240, title, body, accent=color, title_size=22, pad=20))
    out.append(footer("The June Local menu is the second menu; Ventoy is the first. Both are expected."))
    return svg_doc("".join(out))


def wizard_steps(active: int, labels=("Options", "Connect oven", "Install")):
    out = []
    x, y = 130, 210
    for i, label in enumerate(labels):
        color = EMBER if i == active else GREEN if i < active else "#3a3f47"
        out.append(f'<circle cx="{x}" cy="{y}" r="16" fill="{color}"/>')
        out.append(text(x, y + 6, str(i + 1), size=17, weight="700", fill="#160a04", anchor="middle"))
        out.append(text(x + 28, y + 6, label, size=20, fill=FG if i == active else MUTED))
        x += 210
    return "".join(out)


def make_install_options():
    out = [title_header("Connect by name · options", "Reach the oven as june.local, june2.local or june3.local", mockup=True)]
    out.append(wizard_steps(0))
    out.append(text(130, 300, "Install June Local on this oven", size=38, weight="300", fill=FG))
    out.append(paragraph(130, 350, "Adds the project CA to the oven's trust store, so the factory June apps talk to June Local instead of June's cloud. The JuneDns helper advertises the name; if multicast is blocked, add a local DNS entry or use Connect by IP. Install the same CA on the phone afterwards.", width=78, size=22, lh=31))
    out.append(rect(130, 440, 1340, 120, fill=PANEL, rx=14))
    out.append(rect(160, 475, 34, 34, fill=BG2, stroke=LINE, rx=6))
    out.append(text(220, 500, "Keep June Local after a factory reset", size=24, fill=FG))
    out.append(text(220, 532, "Off by default. Stores the June Local patches in /system so a factory reset keeps them.", size=19, fill=MUTED))
    out.append(pill(1360, 486, "OPTIONAL", fill="#2a2e35", fg=EMBER2, size=14))
    out.append(rect(130, 620, 330, 62, fill=EMBER, rx=12))
    out.append(text(295, 660, "Next: connect the oven", size=22, weight="700", fill="#160a04", anchor="middle"))
    out.append(rect(490, 620, 170, 62, fill=BG2, rx=12))
    out.append(text(575, 660, "Back", size=22, fill=MUTED, anchor="middle"))
    out.append(card(130, 720, 1340, 100, "What this does", "Adds the project CA, the local server, the camera bridge and JuneDns. Install the same CA on your phone.", accent=GREEN, title_size=22, pad=20))
    out.append(footer("Connect by IP is the recommended path; Connect by name is for a name instead of an IP."))
    return svg_doc("".join(out))


def make_connect_oven():
    out = [title_header("Put the oven in recovery mode", "Connect the USB cable, hold SW2, then power on", mockup=True)]
    out.append(wizard_steps(1))
    out.append(text(130, 300, "Put the oven in recovery mode", size=38, weight="300", fill=FG))
    # Left illustration card
    out.append(rect(130, 350, 620, 420, fill=PANEL, rx=16))
    out.append(rect(170, 400, 200, 140, fill="#1d2128", stroke=LINE, rx=10))
    out.append(text(270, 465, "computer", size=22, fill=MUTED, anchor="middle"))
    out.append(rect(370, 500, 80, 40, fill="#1d2128", stroke=LINE, rx=8))
    out.append(text(410, 528, "USB", size=18, fill=MUTED, anchor="middle"))
    out.append(f'<path d="M 450 520 C 520 520 540 470 590 470" stroke="{EMBER}" stroke-width="5" fill="none" stroke-dasharray="10 8"/>')
    out.append(rect(560, 370, 150, 280, fill="#1d2128", stroke=LINE, rx=14))
    out.append(text(635, 405, "OVEN", size=20, fill=MUTED, anchor="middle"))
    out.append(f'<circle cx="635" cy="470" r="18" fill="{EMBER}"/>')
    out.append(text(720, 476, "SW2", size=20, fill=FG))
    out.append(f'<circle cx="635" cy="560" r="18" fill="{EMBER2}"/>')
    out.append(text(720, 590, "POWER", size=20, fill=FG))
    out.append(pill(180, 730, "1", fill=EMBER2, fg="#160a04", size=15))
    out.append(pill(700, 430, "2", fill=EMBER2, fg="#160a04", size=15))
    out.append(pill(700, 540, "3", fill=EMBER2, fg="#160a04", size=15))

    # Right instructions
    out.append(text(810, 380, "1. Connect the oven to this computer with the USB cable.", size=23, fill=FG))
    out.append(paragraph(810, 415, "Use a port on the computer itself, not a hub.", width=52, size=20, lh=28))
    out.append(text(810, 500, "2. Press and hold SW2 (force recovery) on the oven's board.", size=23, fill=FG))
    out.append(paragraph(810, 555, "3. Switch the oven's power on and keep holding SW2 for about 10 seconds.", width=52, size=23, lh=30))
    out.append(rect(810, 635, 660, 100, fill="#1b2418", stroke="#31512c", rx=14))
    out.append(text(850, 680, "Oven found · RCM mode", size=26, weight="700", fill=GREEN))
    out.append(text(850, 715, "You can let go of SW2.", size=20, fill=MUTED))
    out.append(rect(810, 750, 330, 62, fill=EMBER, rx=12))
    out.append(text(975, 790, "Install June Local", size=22, weight="700", fill="#160a04", anchor="middle"))
    out.append(rect(1170, 750, 170, 62, fill=BG2, rx=12))
    out.append(text(1255, 790, "Back", size=22, fill=MUTED, anchor="middle"))
    out.append(footer("Once it starts, the oven's fans spin up and stay fast while it works. That is normal."))
    return svg_doc("".join(out))


def make_progress():
    out = [title_header("While the job runs", "Keep the oven connected and powered", mockup=True)]
    out.append(text(130, 240, "Installing June Local", size=24, fill=MUTED))
    out.append(text(130, 340, "42%", size=96, weight="300", fill=FG))
    out.append(text(130, 400, "Writing to the oven · estimating", size=24, fill=EMBER2))
    out.append(rect(130, 440, 1340, 26, fill="#23272e", rx=13))
    out.append(rect(130, 440, 560, 26, fill=EMBER, rx=13))
    out.append(text(130, 510, "3.2 of 7.8 GiB at 9.4 MB/s", size=22, fill=FG))
    out.append(text(130, 545, "This can take about two minutes for an install; a backup or restore takes 15–30 minutes.", size=20, fill=MUTED))
    out.append(rect(130, 600, 1340, 70, fill="#241417", stroke="#4a2226", rx=12))
    out.append(text(170, 645, "Keep the oven connected and powered. Don't unplug anything.", size=23, fill="#ffb4b4"))
    steps = ["Prepare", "Connect oven", "Write image", "Verify", "Finish"]
    x = 200
    for i, s in enumerate(steps):
        color = GREEN if i < 2 else EMBER if i == 2 else "#3a3f47"
        out.append(f'<circle cx="{x}" cy="730" r="14" fill="{color}"/>')
        out.append(text(x + 22, 737, s, size=19, fill=FG if i <= 2 else MUTED))
        x += 250
    out.append(footer("The details panel shows the live installer log; the log is also saved to june-logs/ on the USB."))
    return svg_doc("".join(out))


def make_success():
    out = [title_header("Job finished", "The next steps depend on the job", mockup=True)]
    out.append(text(130, 250, "June Local is installed", size=52, weight="300", fill=GREEN))
    out.append(text(130, 300, "Done · 2:14", size=22, fill=MUTED))
    out.append(rect(130, 350, 1340, 16, fill=GREEN, rx=8))
    steps = [
        "Unplug the oven's power and the USB cable.",
        "Wait 10 seconds, plug the power back in and let the oven start normally.",
        "From a device on the same network, open http://<oven-ip>/local. You should see the June Local status page.",
        "On the phone, install the CA from http://<oven-ip>/local/ca.crt, then pair from the June app or Home Assistant.",
    ]
    y = 430
    for i, s in enumerate(steps):
        out.append(f'<circle cx="{160}" cy="{y-6}" r="10" fill="{EMBER}"/>')
        out.append(paragraph(190, y, s, width=86, size=22, fill=FG, lh=30))
        y += 75
    out.append(rect(130, 780, 260, 58, fill=EMBER, rx=12))
    out.append(text(260, 818, "Done", size=22, weight="700", fill="#160a04", anchor="middle"))
    out.append(footer("Connect by name uses the project CA; Connect by IP uses the per-home CA on the USB."))
    return svg_doc("".join(out))


def make_backup_destination():
    out = [title_header("Back up the oven · destination", "One 7.8 GB file; choose where it goes", mockup=True)]
    out.append(wizard_steps(0, labels=("Drive", "Connect oven", "Back up")))
    out.append(text(130, 310, "Where should the backup go?", size=42, weight="300", fill=FG))
    out.append(paragraph(130, 360, "The backup is one 7.8 GB file. This computer runs from memory, so you can unplug the boot stick and use another drive.", width=88, size=22, lh=31))
    rows = [
        ("Ventoy (current boot USB)", "Boot USB", "exFAT · 12.4 GB free", GREEN),
        ("BackupUSB", "USB", "exFAT · 28.1 GB free", EMBER2),
    ]
    y = 450
    for name, tag, free, color in rows:
        out.append(rect(130, y, 1340, 90, fill=PANEL, rx=14))
        out.append(f'<circle cx="{170}" cy="{y+45}" r="12" fill="{color}"/>')
        out.append(text(200, y + 52, name, size=25, fill=FG))
        out.append(pill(1120, y + 22, tag, fill="#2a2e35", fg=color, size=14))
        out.append(text(1430, y + 52, free, size=20, fill=MUTED, anchor="end"))
        y += 110
    out.append(rect(130, 690, 650, 70, fill=BG2, rx=12))
    out.append(text(160, 733, "Folder (optional):  june-backups", size=21, fill=MUTED, font=MONO))
    out.append(rect(810, 690, 660, 70, fill=BG2, rx=12))
    out.append(text(840, 733, "File name:  261005-1430.img", size=21, fill=MUTED, font=MONO))
    out.append(rect(130, 790, 330, 60, fill=EMBER, rx=12))
    out.append(text(295, 828, "Next: connect the oven", size=21, weight="700", fill="#160a04", anchor="middle"))
    out.append(footer("Leave at least 8 GB free. If the boot stick is full, swap in a second USB now — the live system is already in RAM."))
    return svg_doc("".join(out))


def qr_mock(x, y, size=180):
    # A deterministic QR-like mockup; clearly labeled in the surrounding UI.
    cells = 21
    cell = size / cells
    out = [rect(x, y, size, size, fill="#ffffff", stroke="#ffffff", rx=4)]
    pattern = [
        (0, 0), (1, 0), (2, 0), (3, 0), (4, 0), (6, 0), (7, 0), (9, 0), (10, 0), (12, 0), (14, 0), (15, 0), (16, 0), (18, 0), (20, 0),
        (0, 2), (2, 2), (4, 2), (6, 2), (8, 2), (10, 2), (12, 2), (14, 2), (16, 2), (18, 2), (20, 2),
        (0, 4), (2, 4), (4, 4), (5, 4), (8, 4), (11, 4), (13, 4), (15, 4), (17, 4), (19, 4),
        (0, 6), (1, 6), (3, 6), (5, 6), (7, 6), (9, 6), (11, 6), (13, 6), (15, 6), (17, 6), (19, 6),
        (2, 8), (4, 8), (6, 8), (8, 8), (10, 8), (12, 8), (14, 8), (16, 8), (18, 8), (20, 8),
        (0, 10), (1, 10), (3, 10), (5, 10), (7, 10), (9, 10), (11, 10), (13, 10), (15, 10), (17, 10), (19, 10),
        (2, 12), (4, 12), (6, 12), (8, 12), (10, 12), (12, 12), (14, 12), (16, 12), (18, 12), (20, 12),
        (0, 14), (1, 14), (3, 14), (5, 14), (7, 14), (9, 14), (11, 14), (13, 14), (15, 14), (17, 14), (19, 14),
        (2, 16), (4, 16), (6, 16), (8, 16), (10, 16), (12, 16), (14, 16), (16, 16), (18, 16), (20, 16),
        (0, 18), (1, 18), (3, 18), (5, 18), (7, 18), (9, 18), (11, 18), (13, 18), (15, 18), (17, 18), (19, 18),
        (2, 20), (4, 20), (6, 20), (8, 20), (10, 20), (12, 20), (14, 20), (16, 20), (18, 20), (20, 20),
    ]
    for cx, cy in pattern:
        out.append(f'<rect x="{x + cx * cell}" y="{y + cy * cell}" width="{cell - 0.6}" height="{cell - 0.6}" fill="#050607"/>')
    return "".join(out)


def make_phone_access():
    out = [title_header("Connect a phone", "The phone shows the same screens and can run the jobs", mockup=True)]
    out.append(text(130, 300, "Connect a phone", size=44, weight="300", fill=FG))
    out.append(paragraph(130, 350, "Your phone shows the same screens and can start and follow jobs, and it can download the companion app. It has to be on the same network as this computer.", width=54, size=22, lh=31))
    out.append(rect(130, 500, 640, 300, fill=PANEL, rx=16))
    out.append(qr_mock(180, 545, 200))
    out.append(text(420, 610, "Scan with your phone's camera", size=22, fill=MUTED))
    out.append(text(420, 655, "http://192.168.1.100/", size=22, fill=EMBER2, font=MONO))
    out.append(rect(810, 500, 660, 300, fill=PANEL, rx=16))
    out.append(text(850, 555, "Allow a phone on this network", size=25, weight="700", fill=FG))
    out.append(paragraph(850, 595, "Anyone who scans the code can control the installer. Turning it off disconnects phones and makes a new code.", width=60, size=20, lh=28))
    out.append(rect(850, 700, 100, 46, fill=GREEN, rx=23))
    out.append(f'<circle cx="927" cy="723" r="18" fill="#0b0d10"/>')
    out.append(text(970, 730, "On · 1 phone connected", size=20, fill=MUTED))
    out.append(footer("Use the phone UI to take real screenshots of the home, destination, progress and success screens."))
    return svg_doc("".join(out))


def make_text_mode():
    out = [title_header("Basic text UI", "Fallback when the graphical interface cannot start", mockup=True)]
    out.append(terminal(130, 200, 1340, 560, [
        "June Local installer",
        "",
        "  1) Install June Local",
        "  2) Install with your own CA",
        "  3) Back up the oven",
        "  4) Restore a backup",
        "  5) Exit installer and reboot",
        "",
        "Select an option [1-5]: _",
        "",
        "# Same jobs as the graphical UI.",
        "# Arrow keys / number keys + Enter.",
        "# Logs still go to june-logs/ on the USB.",
    ]))
    out.append(card(130, 790, 1340, 60, None, None, accent=EMBER))
    out.append(text(170, 830, "Use this only if the graphical UI fails. The graphical installer is the normal path.", size=22, fill=MUTED))
    return svg_doc("".join(out))


def make_basic_vs_custom():
    out = [title_header("Connect by IP vs Connect by name", "Pick Connect by IP unless you want to reach the oven by name")]
    out.append(card(90, 200, 680, 420, "Connect by IP", "Recommended. Creates a per-home certificate authority on the USB and a certificate that includes the oven's LAN IP. No hostname or DNS to set up. Install the CA on the phone and use the oven's IP.", accent=EMBER, title_size=30, pad=26))
    out.append(card(830, 200, 680, 420, "Connect by name (june.local)", "Adds the project CA to the oven's trust store. The JuneDns helper advertises june.local, june2.local or june3.local; if multicast is blocked, add a local DNS entry or use Connect by IP. Install the same CA on the phone.", accent=EMBER2, title_size=30, pad=26))
    out.append(rect(90, 660, 1420, 120, fill=PANEL, rx=16))
    out.append(text(130, 710, "Persistence option", size=26, weight="700", fill=FG))
    out.append(text(130, 750, "Off by default. Turn it on to keep June Local after a factory reset; it stores the patches in /system.", size=20, fill=MUTED))
    out.append(footer("Most users should choose Connect by IP."))
    return svg_doc("".join(out))


def make_safety_checklist():
    out = [title_header("Before you press the final button", "A short safety checklist")]
    dos = [
        "Use a USB 3 port on the computer itself, not a hub.",
        "Keep the oven powered and connected for the whole job.",
        "Wait for the success screen before unplugging anything.",
        "Let the oven fans spin fast; that is normal.",
        "Copy the logs from june-logs/ if you need support.",
    ]
    donts = [
        "Do not unplug the oven during an install or restore.",
        "Do not use FAT32 for the backup drive.",
        "Do not rename the Ventoy partition.",
        "Do not close up the oven before testing it.",
        "Do not power off the laptop while the job is running.",
    ]
    out.append(card(90, 200, 680, 520, "Do", None, accent=GREEN, title_size=32, pad=26))
    y = 300
    for item in dos:
        out.append(bullet(130, y, item, width=48, size=21, fill=FG, color=GREEN))
        y += 74
    out.append(card(830, 200, 680, 520, "Don't", None, accent=RED, title_size=32, pad=26))
    y = 300
    for item in donts:
        out.append(bullet(870, y, item, width=48, size=21, fill=FG, color=RED))
        y += 74
    out.append(footer("The installer warns before every destructive step. Nothing changes until you press the final button."))
    return svg_doc("".join(out))


def main() -> int:
    if shutil.which("rsvg-convert") is None:
        print("rsvg-convert not found; install librsvg2-bin or render the SVGs elsewhere", file=sys.stderr)
        return 1
    SVG_DIR.mkdir(parents=True, exist_ok=True)
    PNG_DIR.mkdir(parents=True, exist_ok=True)

    assets = {
        "01-overview-flow": make_overview(),
        "02-ventoy-windows": make_windows(),
        "03-ventoy-linux": make_linux(),
        "04-usb-layout": make_usb_layout(),
        "05-copy-and-config": make_copy_config(),
        "06-boot-menus": make_boot_menus(),
        "07-secure-boot": make_secure_boot(),
        "08-backup-and-swap": make_backup_swap(),
        "09-installer-ui": make_installer_ui(),
        "10-troubleshooting": make_troubleshooting(),
        "11-install-options": make_install_options(),
        "12-connect-oven": make_connect_oven(),
        "13-progress": make_progress(),
        "14-success": make_success(),
        "15-backup-destination": make_backup_destination(),
        "16-phone-access": make_phone_access(),
        "17-text-mode": make_text_mode(),
        "18-basic-vs-custom": make_basic_vs_custom(),
        "19-safety-checklist": make_safety_checklist(),
    }
    for name, doc in assets.items():
        svg_path = SVG_DIR / f"{name}.svg"
        png_path = PNG_DIR / f"{name}.png"
        svg_path.write_text(doc, encoding="utf-8")
        subprocess.run(
            ["rsvg-convert", "-w", "1920", "-o", str(png_path), str(svg_path)],
            check=True,
        )
        print(f"{svg_path.name}  ->  {png_path.name}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
