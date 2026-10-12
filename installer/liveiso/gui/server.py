#!/usr/bin/env python3
"""June Local graphical installer backend.

Serves the web UI and a small JSON + server-sent-events API that drives the
same worker scripts as the text menu (install.sh, backup.sh).  The kiosk
browser on the installer PC talks to it over loopback.  Phones on the LAN can
join once phone access is switched on, using the token carried by the QR code.

Python 3.11 standard library only: this runs on a minimal Debian live image.
"""

from __future__ import annotations

import argparse
import ipaddress
import json
import mimetypes
import os
import re
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import threading
import time
import traceback
from collections import deque
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, quote, urlparse

IMAGE_BYTES = 7818182656
GUIDE_URL = "https://project-july.org/setup"
APX_VENDOR, APX_PRODUCT = "0955", "7740"
UI_MODE_FILE = Path("/run/june-ui-mode")
COOKIE = "june_k"

STAGE_RE = re.compile(r"^\[[*+!-]\] ")
REASON_RE = re.compile(r"^\[-\] ")
DD_RE = re.compile(r"^(\d+) bytes .*copied, ")
RATE_UNITS = {"B/s": 1e-6, "bytes/s": 1e-6, "kB/s": 1e-3, "MB/s": 1.0, "GB/s": 1e3}

# Steps shown in the progress tracker, per action.  A step becomes current
# when one of its patterns shows up in the worker log; steps only move forward.
STEP_LABELS = {
    "certs": "Create certificates",
    "connect": "Connect to oven",
    "rcm": "Load bootloader",
    "emmc": "Open oven storage",
    "deploy": "Install June Local",
    "copy": "Copy oven storage",
    "verify": "Verify backup",
    "write": "Write oven storage",
}
ACTION_STEPS = {
    "install": ["connect", "rcm", "emmc", "deploy"],
    "custom": ["certs", "connect", "rcm", "emmc", "deploy"],
    "backup": ["connect", "rcm", "emmc", "copy", "verify"],
    "restore": ["connect", "rcm", "emmc", "verify", "write"],
}
ACTION_TITLES = {
    "install": "Installing June Local",
    "custom": "Installing June Local with a per-home CA",
    "backup": "Backing up the oven",
    "restore": "Restoring the oven",
}
STEP_PATTERNS = [
    ("connect", re.compile(r"Looking for an existing June UMS disk|Waiting for June APX")),
    ("rcm", re.compile(r"Loading U-Boot with tegrarcm|tegrarcm attempt")),
    ("emmc", re.compile(r"Waiting for the June eMMC|target disk:")),
    ("deploy", re.compile(r"Install log:|^\[\*\] binary:|flashing boot image")),
    ("copy", re.compile(r"Writing backup image to:")),
    ("verify", re.compile(r"Verifying backup")),
    ("write", re.compile(r"^\[\*\] Restoring .* -> ")),
]
# Steps 0..CANCELLABLE_UNTIL only wait for the oven; nothing is written yet.
CANCELLABLE = {"certs", "connect"}

HOST_RE = re.compile(r"^(?=.{1,253}$)([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$")
SAFE_PART_RE = re.compile(r"^[A-Za-z0-9._ -]{1,80}$")


def now() -> float:
    return time.time()


class ApiError(Exception):
    def __init__(self, status: int, message: str):
        super().__init__(message)
        self.status = status
        self.message = message


# --------------------------------------------------------------------------
# Worker log parsing


class LogWatcher:
    """Incrementally reads a worker log and keeps what the UI needs."""

    def __init__(self, path: Path):
        self.path = path
        self.offset = 0
        self.partial = ""
        self.tail: deque[str] = deque(maxlen=300)
        self.reasons: deque[str] = deque(maxlen=3)
        self.stage = ""
        self.steps_seen: set[str] = set()
        self.dd: tuple[int, float] | None = None  # (bytes, MB/s)
        self.dd_at = 0.0

    def poll(self) -> None:
        try:
            with open(self.path, "rb") as fh:
                fh.seek(self.offset)
                data = fh.read(1 << 20)
        except OSError:
            return
        if not data:
            return
        self.offset += len(data)
        text = self.partial + data.decode("utf-8", errors="replace")
        parts = re.split(r"[\r\n]", text)
        self.partial = parts.pop()
        for line in parts:
            self._line(line.rstrip())
        # dd rewrites its progress record with \r and no newline; parse the
        # unterminated record too so progress is live.
        if DD_RE.match(self.partial):
            self._dd(self.partial.rstrip())

    def _dd(self, line: str) -> bool:
        m = DD_RE.match(line)
        if not m:
            return False
        rate = 0.0
        fields = line.split()
        for i in range(len(fields) - 1, 0, -1):
            if fields[i].endswith("/s"):
                try:
                    rate = float(fields[i - 1].replace(",", ".")) * RATE_UNITS.get(fields[i], 0.0)
                except ValueError:
                    rate = 0.0
                break
        self.dd = (int(m.group(1)), rate)
        self.dd_at = now()
        return True

    def _line(self, line: str) -> None:
        if not line:
            return
        if self._dd(line):
            # Collapse consecutive dd records into one log line.
            if self.tail and DD_RE.match(self.tail[-1]):
                self.tail[-1] = line
            else:
                self.tail.append(line)
            return
        self.tail.append(line)
        if STAGE_RE.match(line):
            self.stage = line[4:]
        if REASON_RE.match(line):
            self.reasons.append(line[4:])
        for key, pat in STEP_PATTERNS:
            if pat.search(line):
                self.steps_seen.add(key)

    def progress(self) -> dict | None:
        if not self.dd:
            return None
        done, rate = self.dd
        done = min(done, IMAGE_BYTES)
        eta = int((IMAGE_BYTES - done) / 1e6 / rate) if rate > 0.1 else None
        return {
            "bytes": done,
            "total": IMAGE_BYTES,
            "pct": round(done * 100 / IMAGE_BYTES, 1),
            "rate": round(rate, 1),
            "eta": eta,
        }


# --------------------------------------------------------------------------
# Jobs


class Job:
    def __init__(self, action: str, argv: list[str], log_path: Path, params: dict,
                 stdin_text: str | None, cwd: Path, env: dict | None = None):
        self.id = secrets.token_hex(4)
        self.action = action
        self.params = params
        self.log_path = log_path
        self.started = now()
        self.ended: float | None = None
        self.rc: int | None = None
        self.cancelled = False
        self.saved_to = ""
        self.watch = LogWatcher(log_path)
        self.logfh = open(log_path, "wb")
        self.proc = subprocess.Popen(
            argv,
            cwd=cwd,
            stdin=subprocess.PIPE if stdin_text is not None else subprocess.DEVNULL,
            stdout=self.logfh,
            stderr=subprocess.STDOUT,
            start_new_session=True,
            env=env,
        )
        if stdin_text is not None and self.proc.stdin:
            try:
                self.proc.stdin.write(stdin_text.encode())
                self.proc.stdin.close()
            except OSError:
                pass

    @property
    def running(self) -> bool:
        return self.rc is None

    def step_index(self) -> int:
        steps = ACTION_STEPS[self.action]
        idx = 0
        for i, key in enumerate(steps):
            if key in self.watch.steps_seen:
                idx = i
        return idx

    def cancellable(self) -> bool:
        return self.running and ACTION_STEPS[self.action][self.step_index()] in CANCELLABLE

    def poll(self) -> bool:
        """Read new log output; returns True when the process just ended."""
        self.watch.poll()
        if self.rc is None:
            rc = self.proc.poll()
            if rc is not None:
                self.rc = rc
                self.ended = now()
                self.logfh.close()
                self.watch.poll()
                return True
        return False

    def cancel(self) -> None:
        if not self.running:
            return
        self.cancelled = True
        try:
            os.killpg(self.proc.pid, signal.SIGTERM)
        except OSError:
            pass

    def status(self) -> str:
        if self.running:
            return "running"
        if self.cancelled:
            return "cancelled"
        return "success" if self.rc == 0 else "failed"

    def snapshot(self) -> dict:
        steps = ACTION_STEPS[self.action]
        status = self.status()
        return {
            "id": self.id,
            "action": self.action,
            "title": ACTION_TITLES[self.action],
            "status": status,
            "params": self.params,
            "started": self.started,
            "elapsed": int((self.ended or now()) - self.started),
            "steps": [{"key": k, "label": STEP_LABELS[k]} for k in steps],
            "step": len(steps) if status == "success" else self.step_index(),
            "stage": self.watch.stage,
            "progress": self.watch.progress(),
            "reasons": list(self.watch.reasons),
            "log": list(self.watch.tail)[-200:],
            "cancellable": self.cancellable(),
            "saved_to": self.saved_to,
            "rc": self.rc,
        }


# --------------------------------------------------------------------------
# Application state


class App:
    def __init__(self, args: argparse.Namespace):
        self.args = args
        self.mock = args.mock
        self.payload = Path(args.payload).resolve()
        self.web = Path(args.web).resolve()
        self.port = args.port
        self.lock = threading.RLock()
        self.job: Job | None = None
        self.oven = {"present": False, "mode": None}
        self.mock_oven = False
        self.mock_fail = False
        self.remote_enabled = False
        self.token = secrets.token_urlsafe(9)
        self.remote_clients: dict[int, str] = {}
        self.allowed_dests: set[str] = set()
        self.allowed_images: dict[str, set[str]] = {}
        self.dest_mounts: dict[str, str] = {}
        self.build_id = self._read_payload_stamp("BUILD_ID")
        self.version = self._read_payload_stamp("VERSION")
        self.ips: list[str] = []
        self.ips_at = 0.0
        # --advertise HOST[:PORT]: the address phones should use, when it is
        # not this machine's own (development behind NAT, e.g. WSL).
        host, _, port = (args.advertise or "").partition(":")
        self.advertise_host = host
        self.public_port = int(port) if port else self.port
        threading.Thread(target=self._ticker, daemon=True).start()

    # ---- helpers -------------------------------------------------------

    def _read_payload_stamp(self, name: str) -> str:
        try:
            return (self.payload / name).read_text().strip() or "unknown"
        except OSError:
            return "dev" if self.mock else "unknown"

    def worker(self, name: str) -> list[str]:
        if self.mock:
            return [sys.executable, str(Path(__file__).parent / "dev" / "mock_worker.py"), name]
        return ["bash", str(self.payload / name)]

    def _ticker(self) -> None:
        while True:
            try:
                self._tick()
            except Exception as exc:  # keep the ticker alive no matter what
                print(f"ticker: {exc!r}", file=sys.stderr)
            time.sleep(0.5)

    def _tick(self) -> None:
        oven = self._detect_oven()
        ips = self._detect_addresses() if now() - self.ips_at > 3 else None
        with self.lock:
            self.oven = oven
            if ips is not None:
                self.ips, self.ips_at = ips, now()
            job = self.job
        if job and job.poll():
            threading.Thread(target=self._persist_log, args=(job,), daemon=True).start()

    def _detect_oven(self) -> dict:
        if self.mock:
            busy = self.job is not None and self.job.running and self.job.step_index() >= 2
            if busy:
                return {"present": True, "mode": "ums"}
            return {"present": self.mock_oven, "mode": "apx" if self.mock_oven else None}
        for dev in Path("/sys/bus/usb/devices").glob("*"):
            try:
                if (dev / "idVendor").read_text().strip() == APX_VENDOR and \
                        (dev / "idProduct").read_text().strip() == APX_PRODUCT:
                    return {"present": True, "mode": "apx"}
            except OSError:
                continue
        for blk in Path("/sys/block").glob("sd*"):
            try:
                if int((blk / "size").read_text()) * 512 == IMAGE_BYTES:
                    return {"present": True, "mode": "ums"}
            except (OSError, ValueError):
                continue
        return {"present": False, "mode": None}

    def _persist_log(self, job: Job) -> None:
        """Copy the GUI-side worker log next to the install logs on USB."""
        if self.mock:
            time.sleep(0.5)
            job.saved_to = "/media/ventoy/june-logs/" + job.log_path.name
            return
        targets: list[str] = []
        ventoy = self._run_backup_query(["ventoy-dir"]).strip()
        if ventoy:
            targets.append(ventoy)
        dest = job.params.get("dest", "")
        if dest in self.dest_mounts:
            targets.append(self.dest_mounts[dest])
        elif dest.startswith("/"):
            targets.append(dest)
        targets += ["/run/live/medium", "/lib/live/mount/medium"]
        for mnt in targets:
            try:
                if not os.path.isdir(mnt):
                    continue
                os.makedirs(os.path.join(mnt, "june-logs"), exist_ok=True)
                shutil.copy2(job.log_path, os.path.join(mnt, "june-logs", job.log_path.name))
                certs = self.payload / "certs"
                if certs.is_dir():
                    os.makedirs(os.path.join(mnt, "june-certs"), exist_ok=True)
                    for name in ("ca.crt", "leaf.crt", "fullchain.crt"):
                        if (certs / name).is_file():
                            shutil.copy2(certs / name, os.path.join(mnt, "june-certs", name))
                os.sync()
                job.saved_to = os.path.join(mnt, "june-logs", job.log_path.name)
                return
            except OSError:
                continue

    def _run_backup_query(self, args: list[str], timeout: int = 60) -> str:
        try:
            out = subprocess.run(self.worker("backup.sh") + args, cwd=self.payload,
                                 capture_output=True, text=True, timeout=timeout)
            return out.stdout
        except (OSError, subprocess.TimeoutExpired):
            return ""

    # ---- network / phone access ---------------------------------------

    def is_own_address(self, ip: str) -> bool:
        with self.lock:
            return ip in self.ips or (bool(self.advertise_host) and ip == self.advertise_host)

    def addresses(self) -> list[str]:
        if self.advertise_host:
            return [self.advertise_host]
        with self.lock:
            return list(self.ips)

    def _detect_addresses(self) -> list[str]:
        ips: list[str] = []
        try:
            out = subprocess.run(["ip", "-j", "-4", "addr", "show", "up"],
                                 capture_output=True, text=True, timeout=3).stdout
            for iface in json.loads(out or "[]"):
                name = iface.get("ifname", "")
                if name == "lo" or name.startswith(("docker", "veth", "br-", "virbr")):
                    continue
                for a in iface.get("addr_info", []):
                    ip = a.get("local")
                    if ip and not ipaddress.ip_address(ip).is_loopback:
                        ips.append(ip)
        except (OSError, ValueError, subprocess.TimeoutExpired):
            pass
        if not ips:
            try:
                with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
                    s.connect(("192.0.2.1", 9))
                    ips.append(s.getsockname()[0])
            except OSError:
                pass
        # Prefer private LAN ranges for the QR code.
        ips.sort(key=lambda ip: (not ipaddress.ip_address(ip).is_private, ip))
        return ips

    def phone_url(self, ip: str) -> str:
        port = "" if self.public_port == 80 else f":{self.public_port}"
        return f"http://{ip}{port}/?k={self.token}"

    def file_url(self, ip: str, name: str) -> str:
        port = "" if self.public_port == 80 else f":{self.public_port}"
        return f"http://{ip}{port}/files/{quote(name)}?k={self.token}"

    # ---- state ---------------------------------------------------------

    def files(self) -> dict:
        ca = self.payload / "certs" / "ca.crt"
        apk = self.payload / "companion_endpoint_signed.apk"
        return {"ca.crt": ca.is_file() or self.mock, "companion.apk": apk.is_file() or self.mock}

    def state(self, local: bool) -> dict:
        with self.lock:
            ips = self.addresses()
            st = {
                "build": self.build_id,
                "version": self.version,
                "mock": {"oven": self.mock_oven, "fail": self.mock_fail} if self.mock else None,
                "local": local,
                "guide": GUIDE_URL,
                "oven": dict(self.oven),
                "net": {
                    "ips": ips,
                    "remote": self.remote_enabled,
                    "clients": len(self.remote_clients),
                },
                "files": self.files(),
                "job": self.job.snapshot() if self.job else None,
            }
            if local and self.remote_enabled and ips:
                st["net"]["url"] = self.phone_url(ips[0])
            return st

    # ---- actions -------------------------------------------------------

    def start_job(self, body: dict) -> dict:
        action = body.get("action")
        if action not in ACTION_STEPS:
            raise ApiError(400, "Unknown action.")
        persist = bool(body.get("persist"))
        stdin_text = None
        params: dict = {"persist": persist}
        if action == "install":
            name = str(body.get("name", "")).strip() or "june"
            if not HOST_RE.match(name):
                raise ApiError(400, "The oven name may only contain letters, digits, dots and dashes.")
            params["name"] = name
            # Match the text menu's argument order: prepackaged <name> [persist].
            argv = self.worker("install.sh") + ["prepackaged", name] + (["persist"] if persist else [])
        elif action == "custom":
            ip = str(body.get("ip", "")).strip()
            host = str(body.get("host", "")).strip() or "june.local"
            try:
                ipaddress.IPv4Address(ip)
            except ValueError:
                raise ApiError(400, "Enter the oven's LAN IPv4 address, for example 192.168.1.50.")
            if not HOST_RE.match(host):
                raise ApiError(400, "The hostname may only contain letters, digits, dots and dashes.")
            params.update(ip=ip, host=host)
            argv = self.worker("install.sh") + ["generate", ip, host, "persist" if persist else ""]
        else:
            dest = str(body.get("dest", ""))
            if dest not in self.allowed_dests:
                raise ApiError(400, "Pick a drive from the list (refresh it if you plugged one in).")
            folder = self._clean_folder(str(body.get("folder", "")))
            params.update(dest=dest, folder=folder)
            if action == "backup":
                filename = self._clean_filename(str(body.get("filename", "")))
                params["filename"] = filename
                argv = self.worker("backup.sh") + ["backup", dest, folder, filename]
            else:
                image = str(body.get("image", ""))
                if image not in self.allowed_images.get(f"{dest}\t{folder}", set()):
                    raise ApiError(400, "Pick a backup image from the list.")
                if body.get("confirm") != "RESTORE":
                    raise ApiError(400, "Type RESTORE to confirm.")
                params["image"] = image
                argv = self.worker("backup.sh") + ["restore", dest, folder, image]
                stdin_text = "RESTORE\n"
        with self.lock:
            if self.job and self.job.running:
                raise ApiError(409, "Another job is already running.")
            log_path = Path(f"/tmp/june-gui-{action}-{time.strftime('%Y%m%d-%H%M%S')}.log")
            env = dict(os.environ, MOCK_FAIL="1") if self.mock and self.mock_fail else None
            try:
                self.job = Job(action, argv, log_path, params, stdin_text, self.payload, env)
            except OSError as exc:
                raise ApiError(500, f"Could not start the installer script: {exc.strerror or exc}.")
            return self.job.snapshot()

    @staticmethod
    def _clean_folder(folder: str) -> str:
        """Normalise a folder typed on any platform.

        Both separators mean the same thing: "backups\\2026" and
        "backups/2026" are the nested folder backups/2026.  Empty and parent
        segments are dropped, so the result always stays inside the drive.
        """
        raw = (folder or "").replace("\\", "/")
        parts = []
        for part in raw.split("/"):
            part = part.strip().rstrip(".").strip()
            if not part or part in (".", ".."):
                continue
            if not SAFE_PART_RE.match(part):
                raise ApiError(400, "Use simple folder names (letters, digits, dots, dashes, spaces).")
            parts.append(part)
        if len(parts) > 4:
            raise ApiError(400, "Use at most four folder levels.")
        return "/".join(parts)

    @staticmethod
    def _clean_filename(filename: str) -> str:
        """Take the last path component and make sure it ends in .img.

        People paste paths like "june-backups\\261007-1200.img" or
        "backups/261007-1200.img" into the name field; both separators are
        accepted and only the file name is kept.
        """
        name = (filename or "").replace("\\", "/").rsplit("/", 1)[-1]
        name = name.strip().rstrip(".").strip()
        if not name:
            name = time.strftime("%y%m%d-%H%M.img")
        if not name.lower().endswith(".img"):
            name += ".img"
        if not SAFE_PART_RE.match(name):
            raise ApiError(400, "Use only letters, digits, dots, dashes and spaces in the file name.")
        return name

    def destinations(self) -> dict:
        if self.mock:
            time.sleep(1.2)
            items = [
                {"tag": "/media/ventoy", "label": "Ventoy (current boot USB)", "free": 52_400_000_000, "origin": "boot"},
                {"tag": "/dev/sdc1", "label": "SanDisk Ultra", "free": 61_800_000_000, "origin": "usb"},
                {"tag": "/dev/nvme0n1p3", "label": "nvme0n1", "free": 212_000_000_000, "origin": "internal"},
            ]
        else:
            items = []
            for line in self._run_backup_query(["list"], timeout=90).splitlines():
                fields = line.split("\t")
                if len(fields) < 4 or not fields[0]:
                    continue
                origin = fields[3]
                if "(current boot USB)" in fields[1]:
                    origin = "boot"
                try:
                    free = int(fields[2])
                except ValueError:
                    free = 0
                items.append({"tag": fields[0], "label": fields[1], "free": free, "origin": origin})
            if not any(i["origin"] == "boot" for i in items):
                ventoy = self._run_backup_query(["ventoy-dir"]).strip()
                if ventoy:
                    items.append({"tag": ventoy, "label": "USB boot stick (current boot USB)", "free": 0, "origin": "boot"})
        with self.lock:
            self.allowed_dests = {i["tag"] for i in items}
        return {"items": items}

    def images(self, dest: str, folder: str) -> dict:
        if dest not in self.allowed_dests:
            raise ApiError(400, "Pick a drive from the list first.")
        folder = self._clean_folder(folder)
        if self.mock:
            time.sleep(0.8)
            items = [
                {"name": "251003-1200.img", "size": IMAGE_BYTES},
                {"name": "250914-0930.img", "size": IMAGE_BYTES},
                {"name": "old-partial.img", "size": 2_100_000_000},
            ]
        else:
            items = []
            for line in self._run_backup_query(["images", dest, folder], timeout=90).splitlines():
                fields = line.split("\t")
                if len(fields) == 2 and fields[1].isdigit():
                    items.append({"name": os.path.basename(fields[0]), "size": int(fields[1])})
        for it in items:
            it["ok"] = it["size"] == IMAGE_BYTES
        with self.lock:
            self.allowed_images[f"{dest}\t{folder}"] = {i["name"] for i in items if i["ok"]}
        return {"items": items}

    def set_remote(self, enabled: bool) -> None:
        with self.lock:
            if not enabled:
                # Rotate the token so a scanned code stops working.
                self.token = secrets.token_urlsafe(9)
            self.remote_enabled = enabled

    def qr_svg(self, text: str) -> str | None:
        exe = shutil.which("qrencode")
        if not exe:
            return None
        try:
            out = subprocess.run([exe, "-t", "SVG", "-m", "1", "-l", "M", "-o", "-", text],
                                 capture_output=True, timeout=5)
            return out.stdout.decode() if out.returncode == 0 else None
        except (OSError, subprocess.TimeoutExpired):
            return None

    def file_path(self, name: str) -> Path | None:
        mapping = {
            "ca.crt": self.payload / "certs" / "ca.crt",
            "companion.apk": self.payload / "companion_endpoint_signed.apk",
        }
        p = mapping.get(name)
        if p and p.is_file():
            return p
        return None

    def ui_text_mode(self) -> None:
        if self.mock:
            print("mock: switch to text mode", file=sys.stderr)
            return
        try:
            UI_MODE_FILE.write_text("text\n")
        except OSError:
            pass
        subprocess.run(["pkill", "-x", "cage"], check=False)

    def power(self, action: str) -> None:
        if action not in ("reboot", "poweroff"):
            raise ApiError(400, "Unknown power action.")
        with self.lock:
            if self.job and self.job.running:
                raise ApiError(409, "Wait for the running job to finish first.")
        if self.mock:
            print(f"mock: {action}", file=sys.stderr)
            return
        subprocess.Popen(["systemctl", action])


# --------------------------------------------------------------------------
# HTTP


class Handler(BaseHTTPRequestHandler):
    server_version = "JuneInstaller/1"
    protocol_version = "HTTP/1.1"
    app: App

    def log_message(self, fmt: str, *args) -> None:
        if self.app.args.verbose:
            super().log_message(fmt, *args)

    # ---- access control ------------------------------------------------

    def client_ip(self) -> str:
        """The peer address; with --trust-proxy, a loopback proxy's
        X-Forwarded-For (development only: a forwarder makes every client
        look local, which would skip the phone token)."""
        peer = self.client_address[0]
        fwd = self.headers.get("X-Forwarded-For") if self.app.args.trust_proxy else None
        if fwd and ipaddress.ip_address(peer).is_loopback:
            return fwd.split(",")[-1].strip()
        return peer

    def is_local(self) -> bool:
        """Loopback, or one of this machine's own addresses: a browser on the
        installer PC itself is as trusted as the kiosk.  (A TCP connection
        cannot complete with a spoofed source address, so a LAN client cannot
        pose as one of ours.)"""
        ip = self.client_ip()
        try:
            if ipaddress.ip_address(ip).is_loopback:
                return True
        except ValueError:
            return False
        return self.app.is_own_address(ip)

    def cookie_token(self) -> str:
        for part in (self.headers.get("Cookie") or "").split(";"):
            k, _, v = part.strip().partition("=")
            if k == COOKIE:
                return v
        return ""

    def authorize(self, query: dict) -> bool:
        """Loopback is always trusted; LAN clients need phone access + token."""
        if self.is_local():
            return True
        app = self.app
        token = (query.get("k") or [""])[0] or self.cookie_token()
        self.presented_token = bool(token)
        if not app.remote_enabled:
            return False
        return secrets.compare_digest(token, app.token)

    # ---- responses -----------------------------------------------------

    def send_bytes(self, status: int, body: bytes, ctype: str, extra: dict | None = None) -> None:
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        for k, v in (extra or {}).items():
            self.send_header(k, v)
        if getattr(self, "set_cookie", ""):
            self.send_header("Set-Cookie", self.set_cookie)
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def send_json(self, obj, status: int = 200) -> None:
        self.send_bytes(status, json.dumps(obj).encode(), "application/json")

    def send_error_json(self, status: int, message: str) -> None:
        self.send_json({"error": message}, status)

    def denied_message(self) -> tuple[str, str]:
        if not self.app.remote_enabled:
            return ("Phone access is off",
                    "Turn on <b>Connect a phone</b> on the installer screen, then scan the QR code.")
        if not getattr(self, "presented_token", False):
            return ("Scan the QR code to connect",
                    "Open <b>Phone</b> on the installer screen and scan the code with this device. "
                    "The code lets this device in; the plain address does not.")
        return ("This code no longer works",
                "Scan the QR code that is on the installer screen now. A new code is made each time "
                "phone access is turned off or the installer restarts.")

    def denied_page(self) -> None:
        title, text = self.denied_message()
        html = (
            "<!doctype html><meta name=viewport content='width=device-width,initial-scale=1'>"
            "<title>June Local installer</title>"
            "<body style='font-family:system-ui;background:#0d1117;color:#e6edf3;display:grid;"
            "place-items:center;min-height:90vh;text-align:center;padding:24px'>"
            f"<div><h2>{title}</h2><p style='color:#9aa7b4'>{text}</p></div>"
        )
        self.send_bytes(403, html.encode(), "text/html; charset=utf-8")

    def read_json(self) -> dict:
        length = int(self.headers.get("Content-Length") or 0)
        if length > 65536:
            raise ApiError(413, "Request too large.")
        if "application/json" not in (self.headers.get("Content-Type") or ""):
            raise ApiError(415, "Expected JSON.")
        try:
            body = json.loads(self.rfile.read(length) or b"{}")
        except json.JSONDecodeError:
            raise ApiError(400, "Malformed JSON.")
        if not isinstance(body, dict):
            raise ApiError(400, "Expected a JSON object.")
        return body

    # ---- routing -------------------------------------------------------

    def do_HEAD(self) -> None:
        self.do_GET()

    def do_GET(self) -> None:
        url = urlparse(self.path)
        query = parse_qs(url.query)
        self.set_cookie = ""
        if not self.authorize(query):
            if url.path.startswith("/api/"):
                return self.send_error_json(403, self.denied_message()[0] + ".")
            return self.denied_page()
        # First visit from the QR code: answer directly and store the token in
        # a cookie (the page then drops ?k= from the address bar).  No
        # redirect, and Lax rather than Strict: a link opened from the phone's
        # camera app is a cross-site navigation, and Strict cookies are not
        # sent on it, not even after a same-site redirect.
        if not self.is_local() and "k" in query:
            self.set_cookie = f"{COOKIE}={self.app.token}; Path=/; HttpOnly; SameSite=Lax"
        try:
            if url.path == "/api/state":
                return self.send_json(self.app.state(self.is_local()))
            if url.path == "/api/events":
                return self.events()
            if url.path == "/api/destinations":
                return self.send_json(self.app.destinations())
            if url.path == "/api/images":
                return self.send_json(self.app.images((query.get("dest") or [""])[0],
                                                      (query.get("folder") or [""])[0]))
            if url.path == "/api/log":
                return self.job_log()
            if url.path == "/api/qr":
                return self.qr((query.get("for") or ["phone"])[0])
            if url.path.startswith("/files/"):
                return self.download(url.path[len("/files/"):])
            return self.static(url.path)
        except ApiError as exc:
            return self.send_error_json(exc.status, exc.message)
        except Exception:  # never drop the connection without an answer
            traceback.print_exc()
            return self.send_error_json(500, "Internal error in the installer backend.")

    def do_POST(self) -> None:
        url = urlparse(self.path)
        if not self.authorize({}):
            return self.send_error_json(403, self.denied_message()[0] + ".")
        app = self.app
        try:
            body = self.read_json()
            if url.path == "/api/job":
                return self.send_json(app.start_job(body))
            if url.path == "/api/job/cancel":
                with app.lock:
                    if not app.job or not app.job.cancellable():
                        raise ApiError(409, "This job can no longer be cancelled safely.")
                    app.job.cancel()
                return self.send_json({"ok": True})
            if url.path == "/api/job/dismiss":
                with app.lock:
                    if app.job and app.job.running:
                        raise ApiError(409, "The job is still running.")
                    app.job = None
                return self.send_json({"ok": True})
            # Everything below changes the installer PC itself: kiosk only.
            if not self.is_local():
                raise ApiError(403, "Only the installer screen can do that.")
            if url.path == "/api/remote":
                app.set_remote(bool(body.get("enabled")))
                return self.send_json(app.state(True)["net"])
            if url.path == "/api/ui/text":
                app.ui_text_mode()
                return self.send_json({"ok": True})
            if url.path == "/api/power":
                app.power(str(body.get("action")))
                return self.send_json({"ok": True})
            if url.path == "/api/mock/oven" and app.mock:
                if "present" in body:
                    app.mock_oven = bool(body["present"])
                if "fail" in body:
                    app.mock_fail = bool(body["fail"])
                return self.send_json({"ok": True})
            raise ApiError(404, "Not found.")
        except ApiError as exc:
            return self.send_error_json(exc.status, exc.message)
        except Exception:  # never drop the connection without an answer
            traceback.print_exc()
            return self.send_error_json(500, "Internal error in the installer backend.")

    # ---- endpoints -----------------------------------------------------

    def events(self) -> None:
        """Server-sent events: push the full state whenever it changes."""
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Connection", "keep-alive")
        self.end_headers()
        app = self.app
        local = self.is_local()
        key = id(self)
        if not local:
            with app.lock:
                app.remote_clients[key] = self.client_ip()
        last = ""
        last_sent = 0.0
        try:
            while True:
                if not local and not app.remote_enabled:
                    break
                payload = json.dumps(app.state(local))
                if payload != last:
                    self.wfile.write(f"data: {payload}\n\n".encode())
                    self.wfile.flush()
                    last, last_sent = payload, now()
                elif now() - last_sent > 15:
                    self.wfile.write(b": ping\n\n")
                    self.wfile.flush()
                    last_sent = now()
                time.sleep(0.5)
        except (BrokenPipeError, ConnectionResetError, OSError):
            pass
        finally:
            with app.lock:
                app.remote_clients.pop(key, None)
            self.close_connection = True

    def job_log(self) -> None:
        job = self.app.job
        if not job:
            raise ApiError(404, "No job log.")
        try:
            data = job.log_path.read_bytes()[-2_000_000:]
        except OSError:
            raise ApiError(404, "Log not readable.")
        self.send_bytes(200, data, "text/plain; charset=utf-8",
                        {"Content-Disposition": f'inline; filename="{job.log_path.name}"'})

    def qr(self, target: str) -> None:
        app = self.app
        ips = app.addresses()
        if target == "guide":
            text = GUIDE_URL
        elif not self.is_local():
            raise ApiError(403, "Only the installer screen shows pairing codes.")
        elif target == "oven":
            params = getattr(app.job, "params", None) or {}
            # Prefer the LAN IP a custom install used for the certificate; it
            # resolves even where mDNS (*.local) does not.
            host = str(params.get("ip") or "").strip()
            if not host:
                host = str(params.get("host") or "").strip()
            if not host:
                name = str(params.get("name") or "june").strip().rstrip(".") or "june"
                host = name if name.endswith(".local") else name + ".local"
            text = f"http://{host}/local"
        elif not app.remote_enabled or not ips:
            raise ApiError(409, "Phone access is off or there is no network.")
        elif target == "phone":
            text = app.phone_url(ips[0])
        elif target in ("ca.crt", "companion.apk"):
            text = app.file_url(ips[0], target)
        else:
            raise ApiError(404, "Unknown QR target.")
        svg = app.qr_svg(text)
        if svg is None:
            raise ApiError(501, "qrencode is not installed.")
        self.send_bytes(200, svg.encode(), "image/svg+xml")

    def download(self, name: str) -> None:
        path = self.app.file_path(name)
        if not path and self.app.mock and name in ("ca.crt", "companion.apk"):
            return self.send_bytes(200, f"mock {name}\n".encode(), "text/plain",
                                   {"Content-Disposition": f'attachment; filename="mock-{name}.txt"'})
        if not path:
            raise ApiError(404, "That file is not available on this installer.")
        ctype = "application/vnd.android.package-archive" if name.endswith(".apk") else "application/x-x509-ca-cert"
        filename = "june-local-ca.crt" if name == "ca.crt" else "june-companion.apk"
        self.send_bytes(200, path.read_bytes(), ctype,
                        {"Content-Disposition": f'attachment; filename="{filename}"'})

    def static(self, path: str) -> None:
        if path in ("", "/"):
            path = "/index.html"
        target = (self.app.web / path.lstrip("/")).resolve()
        if self.app.web not in target.parents or not target.is_file():
            raise ApiError(404, "Not found.")
        ctype = mimetypes.guess_type(target.name)[0] or "application/octet-stream"
        if ctype.startswith("text/") or ctype in ("application/javascript", "image/svg+xml"):
            ctype += "; charset=utf-8"
        self.send_bytes(200, target.read_bytes(), ctype)


def main() -> None:
    here = Path(__file__).resolve().parent
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--port", type=int, default=80)
    ap.add_argument("--bind", default="0.0.0.0")
    ap.add_argument("--payload", default="/opt/june-installer")
    ap.add_argument("--web", default=str(here / "web"))
    ap.add_argument("--mock", action="store_true", help="simulate the oven and workers (development)")
    ap.add_argument("--verbose", action="store_true")
    ap.add_argument("--advertise", metavar="HOST[:PORT]",
                    help="address to put in the phone QR code (development behind NAT)")
    ap.add_argument("--trust-proxy", action="store_true",
                    help="take the client address from X-Forwarded-For sent by a loopback proxy (development)")
    args = ap.parse_args()

    mimetypes.add_type("application/javascript", ".js")
    mimetypes.add_type("image/svg+xml", ".svg")
    Handler.app = App(args)
    server = ThreadingHTTPServer((args.bind, args.port), Handler)
    server.daemon_threads = True
    print(f"June Local GUI backend on http://{args.bind}:{args.port}/"
          f"{' (mock mode)' if args.mock else ''}", file=sys.stderr)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
