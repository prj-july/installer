/* June USB.  Plain script, no build step.  The Go side (app.go) is
   window.go.main.App; progress arrives as Wails events.  Only when opened
   in a browser with "?mock" in the address (design work, never inside the
   app) does it run against a mock backend with made-up drives. */
(() => {
"use strict";

const $ = (sel, root = document) => root.querySelector(sel);
const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

/* ---- Marks and icons (as in the installer's app.js) -------------------- */

const MARK = {
  wait: '<svg class="mark m-wait" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" stroke-dasharray="3 3.1"/></svg>',
  ok: '<svg class="mark m-ok" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M8 12.4l2.8 2.8L16.2 9.6"/></svg>',
  done: '<svg class="mark m-ok solid" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="10"/><path d="M7.6 12.4l3 3 5.8-5.8" stroke-width="2.6"/></svg>',
  run: '<svg class="mark m-run" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" opacity=".25"/><path class="spin" d="M12 3a9 9 0 0 1 9 9"/></svg>',
  warn: '<svg class="mark m-warn" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3.6 2.8 19.6h18.4z"/><path d="M12 10v4.2M12 17.1v.01"/></svg>',
};
const CHECK = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';
const CHEVRON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 6l6 6-6 6"/></svg>';
const icon = (id, cls = "") => `<svg${cls ? ` class="${cls}"` : ""} viewBox="0 0 24 24" aria-hidden="true"><use href="#i-${id}"/></svg>`;

const WIZARD = ["Installer", "USB stick", "Confirm", "Write"];

/* ---- Backend ----------------------------------------------------------- */

// Wails' runtime script (injected into <head>) defines window.go and
// window.runtime.  This whole file is wrapped in a function so none of its
// names can replace them: a top-level "function go()" once did, and the app
// silently ran on the mock.  The short wait is only a safety margin.  The
// mock must be asked for explicitly (?mock) and is labelled on screen.
const PREVIEW = new URLSearchParams(location.search).has("mock");
let api = null;
let on = null;

async function connect() {
  if (PREVIEW) {
    api = mockApi();
    on = mockOn;
    // Docs screenshots (?mock&screen=...) leave the banner out.
    if (!new URLSearchParams(location.search).has("screen")) {
      document.body.insertAdjacentHTML("beforeend", '<div class="dev">DESIGN PREVIEW · FAKE DRIVES · NOTHING IS WRITTEN</div>');
    }
    return;
  }
  for (let i = 0; i < 200 && !(window.go && window.go.main && window.go.main.App && window.runtime); i++) {
    await new Promise((r) => setTimeout(r, 50));
  }
  if (!(window.go && window.go.main && window.go.main.App && window.runtime)) {
    const seen = `go=${Object.keys(window.go || {}).join(",") || "none"} runtime=${!!window.runtime} ipc=${typeof window.WailsInvoke} url=${location.href}`;
    try { window.runtime.LogError("June USB: backend not found: " + seen); } catch (e) { /* no runtime */ }
    throw new Error("the app could not connect to its backend (" + seen + "); please restart June USB");
  }
  api = window.go.main.App;
  on = (name, fn) => window.runtime.EventsOn(name, fn);
}

/* ---- State ------------------------------------------------------------- */

const ui = {
  view: "release",
  info: { guideUrl: "https://project-july.org/setup" },
  release: null, releaseErr: "", releaseLoading: true,
  releases: [], pick: 0, olderOpen: false, // releases.json, newest first; pick indexes it (0 = latest)
  iso: null, isoErr: "",
  bytes: null,           // {done, total, phase, t0}
  drives: null, drivesErr: "", showAll: false, sel: "",
  mode: "fresh", ack: false,
  steps: [], progress: null, log: [], logOpen: false, error: "",
  result: { ejected: false, ejectError: "" },
};

const gb = (n) => (n / 1e9).toFixed(1) + " GB";
const mb = (n) => Math.round(n / 1e6) + " MB";
const size = (n) => (n >= 1e9 ? gb(n) : mb(n));
const selected = () => (ui.drives || []).find((d) => d.id === ui.sel);
// How the user knows the stick: its volume labels, else its model (as stickName in app.go).
const stickName = (d) => {
  const labels = (d.volumes || []).filter((v) => v.label).map((v) => `“${v.label}”`);
  return labels.length ? labels.join(" and ") : d.name;
};

/* ---- Appearance: system, light or dark --------------------------------- */

// index.html applies a saved choice before the first paint; this keeps the
// switch, the saved value and (on Windows) the title bar in step.
function look(v, save = true) {
  if (v !== "light" && v !== "dark") v = "system";
  if (v === "system") delete document.documentElement.dataset.look;
  else document.documentElement.dataset.look = v;
  document.querySelectorAll('[data-do="look"]').forEach((b) => b.setAttribute("aria-checked", String(b.dataset.v === v)));
  if (save) { try { localStorage.setItem("look", v); } catch (e) { /* not kept */ } }
  try {
    const rt = !PREVIEW && window.runtime;
    if (rt) (v === "light" ? rt.WindowSetLightTheme : v === "dark" ? rt.WindowSetDarkTheme : rt.WindowSetSystemDefaultTheme)();
  } catch (e) { /* title bar themes are Windows only */ }
}
function savedLook() {
  try { return localStorage.getItem("look") || "system"; } catch (e) { return "system"; }
}

function errText(e) {
  const s = e && e.message ? e.message : String(e || "Unknown error");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/* ---- Rendering --------------------------------------------------------- */

function wizard(stepIdx) {
  return `<ol class="wizard" aria-label="Steps">${WIZARD.map((label, i) => `
    <li class="${i < stepIdx ? "done" : i === stepIdx ? "on" : ""}"${i === stepIdx ? ' aria-current="step"' : ""}>
      <span class="n">${i < stepIdx ? CHECK.replace("<svg", '<svg style="width:14px;height:14px;fill:none;stroke:currentColor;stroke-width:3;stroke-linecap:round;stroke-linejoin:round"') : i + 1}</span><span>${esc(label)}</span>
    </li>`).join("")}</ol>`;
}

function opt(name, title, sub, checked, { radio = false, value = "", side = "", lead = "", off = false } = {}) {
  return `<label class="opt${checked ? " sel" : ""}${off ? " off" : ""}">
    <input type="${radio ? "radio" : "checkbox"}" name="${name}" value="${esc(value)}"${checked ? " checked" : ""}${off ? " disabled" : ""}>
    ${lead || `<span class="box${radio ? " round" : ""}">${CHECK}</span>`}
    <span class="opt-main"><span class="opt-title">${title}</span>${sub ? `<span class="opt-sub">${sub}</span>` : ""}</span>
    ${side ? `<span class="opt-side">${side}</span>` : ""}
  </label>`;
}

let shown = "";
function render() {
  const main = $("#main");
  const html = VIEWS[ui.view]();
  // Fade in only when the screen changes, not on every progress update.
  main.innerHTML = `<div class="view${ui.view !== shown ? " enter" : ""}">${html}</div>`;
  shown = ui.view;
  chip();
  if (ui.view === "run") updateRun();
}

function chip() {
  const c = $("#iso-chip");
  if (ui.iso) {
    c.hidden = false;
    c.innerHTML = `${ui.iso.verified ? MARK.ok : MARK.warn}<span>${esc(ui.iso.version)}</span>`;
  } else c.hidden = true;
}

function flow(step, body) {
  return `<div class="flow">${wizard(step)}<div>${body}</div></div>`;
}

// The latest release as a big card, the older ones from releases.json as
// small rows under it.  With only latest.json it is the single card, as before.
function releaseList() {
  const rs = ui.releases.length ? ui.releases : [ui.release];
  const when = (r) => (r.date ? new Date(r.date + "T12:00:00").toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" }) : "");
  const sub = (r) => [size(r.size), when(r)].filter(Boolean).join(" · ");
  const one = rs.length === 1;
  const latest = `<label class="rel${one ? "" : " rel-latest"}${ui.pick === 0 && !one ? " sel" : ""}">
    ${one ? "" : `<input type="radio" name="release" value="0"${ui.pick === 0 ? " checked" : ""}>`}
    <div class="rel-icon">${icon("down")}</div>
    <div class="rel-main">
      <div class="rel-title">June Local ${esc(rs[0].version)}${one ? "" : `<span class="pill ok">Latest</span>`}</div>
      <div class="rel-sub">${sub(rs[0])} · downloads.project-july.org · checked with SHA256${rs[0].sha256 ? ` <span class="sha" title="SHA256 ${esc(rs[0].sha256)}">${esc(rs[0].sha256.slice(0, 8))}</span>` : ""}</div>
    </div>
    ${one ? "" : `<span class="box round">${CHECK}</span>`}
  </label>`;
  if (one) return latest;
  const older = rs.slice(1).map((r, i) => opt("release", `June Local ${esc(r.version)}`, sub(r), ui.pick === i + 1,
    { radio: true, value: String(i + 1), side: `<span class="sha" title="SHA256 ${esc(r.sha256)}">${esc((r.sha256 || "").slice(0, 8))}</span>` })).join("");
  const note = ui.pick ? `<div class="warnline older-note">${MARK.warn}<span>${esc(rs[ui.pick].version)} is not the latest version. Pick it only if the latest one does not work for you.</span></div>` : "";
  return `${latest}
    <details class="older"${ui.pick || ui.olderOpen ? " open" : ""}>
      <summary>${CHEVRON}Older versions</summary>
      <div class="older-list">${older}</div>
    </details>${note}`;
}

// An ARM computer can make the stick but can't start the amd64 installer.
function armNote() {
  if (!ui.info.arm) return "";
  const what = ui.info.os === "darwin" ? "This Mac has an Apple chip" : "This computer has an ARM processor";
  return `<div class="warnline">${MARK.warn}<span><b>${what}, so it can't start the installer.</b> It can make the stick; start the installer on a laptop with an Intel or AMD processor.</span></div>`;
}

const VIEWS = {
  release() {
    let card;
    if (ui.releaseLoading) card = `<div class="skel"></div>`;
    else if (ui.release) card = releaseList();
    else {
      card = `<div class="warnline">${MARK.warn}<span>Could not check for the latest release: ${esc(ui.releaseErr)}<br>You can still use an ISO file you downloaded before.</span></div>`;
    }
    const b = ui.bytes;
    let prog = "";
    if (b) {
      const frac = b.total ? b.done / b.total : 0;
      const secs = (Date.now() - b.t0) / 1000;
      const rate = secs > 1 ? b.done / secs : 0;
      const left = rate > 0 ? Math.max(0, (b.total - b.done) / rate) : 0;
      const what = b.phase === "download" ? "Downloading" : "Checking the file";
      prog = `<div class="rel-prog" id="rel-prog">
        <div class="bar"><i style="--p:${frac.toFixed(4)}"></i></div>
        <div class="facts"><b>${what}</b> · ${size(b.done)} of ${size(b.total)}${rate ? ` · ${Math.round(rate / 1e6)} MB/s` : ""}${left > 2 ? ` · about ${fmtLeft(left)} left` : ""}</div>
      </div>`;
    }
    const busy = !!b;
    const err = ui.isoErr ? `<div class="err">${MARK.warn}<span>${esc(ui.isoErr)}</span></div>` : "";
    return flow(0, `
      <p class="eyebrow">Step 1 of 3</p>
      <h1>Get the installer</h1>
      <p class="muted">June USB downloads the latest June Local installer and puts it on a USB stick.
        The stick also keeps room for oven backups, logs and certificates.</p>
      <div class="${ui.releases.length > 1 ? "rels" : "panel"}" style="margin-top:22px">${card}${prog}</div>
      ${err}${armNote()}
      <div class="actions">
        ${busy ? `<button class="btn ghost" type="button" data-do="cancel">Cancel</button>`
               : `<button class="btn ember" type="button" data-do="download"${ui.release ? "" : " disabled"}>${icon("down")}${ui.pick ? `Download ${esc(ui.release.version)} and continue` : "Download and continue"}</button>
                  <button class="link" type="button" data-do="choose">Use an ISO file I already have</button>`}
      </div>
      <p class="foot">June USB ${esc(ui.info.version || "")}</p>`);
  },

  drives() {
    const ds = ui.drives;
    let list;
    if (ds == null) list = `<div class="skel" style="margin:18px 0"></div>`;
    else if (!ds.length) {
      list = `<div class="waiting">${MARK.run}<div><div class="detect-word">Waiting for a USB stick</div>
        <div class="detect-sub">Plug in a USB stick of 16 GB or more.${ui.showAll ? "" : " Not listed? Try “Show all drives”."}</div></div></div>`;
    } else {
      list = `<div class="opts">${ds.map((d) => {
        const vols = (d.volumes || []).filter((v) => v.label || v.mount).map((v) => [v.mount, v.label].filter(Boolean).join(" ")).join(", ");
        const sub = [size(d.size), d.bus, vols || "no volumes"].filter(Boolean).join(" · ");
        const side = !d.eligible ? `<span class="pill warn">Can't use</span>` : d.juneStick ? `<span class="pill ok">June stick</span>` : "";
        const title = esc(d.name) + (!d.eligible ? `<span class="opt-sub">${esc(d.reason)}</span>` : "");
        return opt("drive", title, esc(sub), d.id === ui.sel, {
          radio: true, value: d.id, side, off: !d.eligible,
          lead: `<span class="drv-icon">${icon("usb")}</span>`,
        });
      }).join("")}</div>`;
    }
    const err = ui.drivesErr ? `<div class="err">${MARK.warn}<span>${esc(ui.drivesErr)}</span></div>` : "";
    return flow(1, `
      <p class="eyebrow">Step 2 of 3</p>
      <h1>Choose the USB stick</h1>
      <p class="muted">16 GB is enough for the installer and one oven backup; 32 GB is recommended.</p>
      ${list}${err}
      <button class="switch" type="button" role="switch" aria-checked="${ui.showAll}" data-do="showall">
        <span><span class="opt-title">Show all drives</span><span class="opt-sub">Also lists SD cards and drives over 300 GB. Built-in disks are never shown.</span></span>
        <span class="track"></span>
      </button>
      <div class="actions">
        <button class="btn ghost" type="button" data-do="back">Back</button>
        <button class="btn" type="button" data-do="next"${selected() && selected().eligible ? "" : " disabled"}>Continue</button>
      </div>`);
  },

  confirm() {
    const d = selected();
    if (!d) return "";
    const vols = (d.volumes || []).filter((v) => v.label || v.mount);
    const update = d.juneStick && ui.mode === "update";
    let choose = "";
    if (d.juneStick) {
      choose = `<div class="opts">
        ${opt("mode", "Update the installer", "Replaces the installer and keeps everything on JUNE: oven backups, logs and certificates.", ui.mode === "update", { radio: true, value: "update" })}
        ${opt("mode", "Erase and start fresh", "Deletes everything on the stick, including oven backups.", ui.mode === "fresh", { radio: true, value: "fresh" })}
      </div>`;
    }
    const layout = `<div class="layout" aria-hidden="true">
      <div class="lb"><b>JUNEBOOT</b><span>4.3 GB · installer${update ? " · replaced" : ""}</span></div>
      <div class="ld"><b>JUNE</b><span>${size(d.size - 4.3e9)} · backups, logs, certificates${update ? " · kept" : ""}</span></div>
    </div>`;
    let warn = "";
    if (!update) {
      const list = vols.length ? `: ${vols.map((v) => esc([v.mount, v.label ? `“${v.label}”` : ""].filter(Boolean).join(" "))).join(", ")}` : "";
      warn = `<div class="warnline">${MARK.warn}<span><b>Everything on ${esc(d.name)} (${size(d.size)}) will be erased${list}.</b> This cannot be undone.</span></div>
        <div class="opts">${opt("ack", `I have copied everything I need from ${esc(stickName(d))}`, "", ui.ack)}</div>`;
    }
    const iso = ui.iso;
    const isoLine = `${esc(iso.version)}${iso.verified ? "" : " (not the published release)"}${iso.bios ? "" : " · UEFI only (this ISO has no legacy BIOS boot code)"}`;
    return flow(2, `
      <p class="eyebrow">Step 3 of 3</p>
      <h1>${update ? "Update this June stick" : `Erase ${esc(stickName(d))}?`}</h1>
      <p class="muted">${esc(d.name)} · ${size(d.size)} · ${esc(d.bus)}<br>Installer: ${isoLine}</p>
      ${choose}${layout}${warn}
      <div class="actions">
        <button class="btn ghost" type="button" data-do="back">Back</button>
        ${update ? `<button class="btn ember" type="button" data-do="write">Update the stick</button>`
                 : `<button class="btn danger" type="button" data-do="write"${ui.ack ? "" : " disabled"}>Erase and create the stick</button>`}
      </div>`);
  },

  run() {
    return `
      <div class="run">
        <div>
          <p class="eyebrow">${ui.mode === "update" ? "Updating" : "Creating"} the June stick</p>
          <div class="hero" id="r-hero">0<small>%</small></div>
          <div class="st" id="r-st"></div>
          <div class="bar" id="r-bar"><i></i></div>
          <div class="facts" id="r-facts"></div>
          <div class="keep">${MARK.warn}<span>Don't unplug the stick or close this window.</span></div>
          ${logDetails()}
        </div>
        <ol class="tracker" id="r-tracker" aria-label="Progress"></ol>
      </div>`;
  },

  done() {
    const iso = ui.iso || {};
    return `
      <p class="eyebrow">${MARK.done}<span>Done</span></p>
      <h1>Your stick is ready</h1>
      ${ui.result.ejected
        ? `<p class="muted">It has been ejected; you can unplug it now.${iso.bios ? "" : " It starts on UEFI computers; this ISO has no legacy BIOS boot code."}</p>`
        : `<div class="warnline">${MARK.warn}<span><b>Eject it before unplugging.</b> Everything is written and checked, but ${esc(ui.result.ejectError || "the stick could not be ejected automatically")}. Close any windows showing the stick, then use “Eject” in File Explorer (or Finder).</span></div>`}
      ${armNote()}
      <ol class="done-list">
        <li>Plug the stick into the laptop that will run the installer (not the oven).</li>
        <li>Turn the laptop on and open its boot menu. The key is usually F12, F11, Esc or F2.</li>
        <li>Choose the USB stick. If it is listed twice, pick the “UEFI” entry.</li>
        <li>Choose “Install June Local - graphical” and follow the installer.</li>
      </ol>
      <div class="actions">
        <button class="btn" type="button" data-do="guide">Open the setup guide</button>
        <button class="btn ghost" type="button" data-do="again">Prepare another stick</button>
      </div>
      ${logDetails()}`;
  },

  error() {
    return `
      <p class="eyebrow">${MARK.warn}<span>Stopped</span></p>
      <h1>That didn't work</h1>
      <p class="sel-text">${esc(ui.error)}</p>
      <p class="muted small">Nothing else on your computer was changed. You can try again; if it keeps failing, try another USB port or stick.</p>
      <div class="actions">
        <button class="btn" type="button" data-do="again">Start over</button>
      </div>
      ${logDetails()}`;
  },
};

function logDetails() {
  return `<details class="log" id="r-log"${ui.logOpen ? " open" : ""}><summary>${CHEVRON}Show details</summary><div class="logbox" id="r-logbox">${ui.log.map(esc).join("\n")}</div></details>`;
}

function fmtLeft(s) {
  if (s < 60) return `${Math.round(s)} s`;
  return `${Math.round(s / 60)} min`;
}

function updateRun() {
  const p = ui.progress || { step: 0, fraction: -1, overall: 0, detail: "" };
  const set = (id, html) => { const el = document.getElementById(id); if (el && el.innerHTML !== html) el.innerHTML = html; };
  set("r-hero", `${Math.floor(p.overall * 100)}<small>%</small>`);
  set("r-st", `${MARK.run}<span>${esc(ui.steps[p.step] || "Working")}</span>`);
  const bar = $("#r-bar");
  if (bar) bar.style.setProperty("--p", p.overall.toFixed(4));
  set("r-facts", esc(p.detail || ""));
  set("r-tracker", ui.steps.map((s, i) => {
    const cls = i < p.step ? "done" : i === p.step ? "on" : "";
    return `<li class="${cls}"${cls === "on" ? ' aria-current="step"' : ""}><span class="n">${i < p.step ? CHECK : ""}</span><span>${esc(s)}</span></li>`;
  }).join(""));
  const box = $("#r-logbox");
  if (box) {
    const atEnd = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
    const text = ui.log.join("\n");
    if (box.textContent !== text) box.textContent = text;
    if (atEnd) box.scrollTop = box.scrollHeight;
  }
}

/* ---- Actions ----------------------------------------------------------- */

let drivePoll = 0;

function show(view) {
  ui.view = view;
  clearInterval(drivePoll);
  if (view === "drives") {
    refreshDrives();
    drivePoll = setInterval(refreshDrives, 2000);
  }
  render();
}

async function loadRelease() {
  ui.releaseLoading = true;
  render();
  try {
    // releases.json lists every published version; offer the last three.
    // If that list cannot be read it is latest.json alone, as before.
    try { ui.releases = (await api.Releases()).slice(0, 3); } catch (e) { ui.releases = []; }
    ui.release = ui.releases.length ? ui.releases[0] : await api.Release();
    ui.pick = 0;
    ui.releaseErr = "";
  } catch (e) {
    ui.release = null;
    ui.releases = [];
    ui.releaseErr = errText(e);
  }
  ui.releaseLoading = false;
  render();
}

async function getISO(how) {
  ui.isoErr = "";
  ui.bytes = { done: 0, total: 0, phase: how === "download" ? "download" : "check", t0: Date.now() };
  render();
  try {
    const iso = how === "download" ? await api.Download(ui.release.version) : await api.ChooseISO();
    ui.bytes = null;
    if (!iso) { render(); return; } // dialog cancelled
    ui.iso = iso;
    show("drives");
  } catch (e) {
    ui.bytes = null;
    const msg = errText(e);
    ui.isoErr = msg === "Cancelled" ? "" : msg;
    render();
  }
}

let refreshing = false;
async function refreshDrives() {
  if (refreshing) return;
  refreshing = true;
  try {
    const ds = await api.Drives(ui.showAll);
    const before = JSON.stringify(ui.drives);
    ui.drives = ds || [];
    ui.drivesErr = "";
    if (ui.sel && !ui.drives.some((d) => d.id === ui.sel && d.eligible)) ui.sel = "";
    if (!ui.sel) {
      const ok = ui.drives.filter((d) => d.eligible);
      if (ok.length === 1) ui.sel = ok[0].id;
    }
    if (ui.view === "drives" && JSON.stringify(ui.drives) !== before) render();
  } catch (e) {
    ui.drivesErr = errText(e);
    if (ui.view === "drives") render();
  } finally {
    refreshing = false;
  }
}

async function write() {
  const d = selected();
  if (!d) return;
  const mode = d.juneStick ? ui.mode : "fresh";
  ui.mode = mode;
  // Erasing always ends with a system dialog naming this stick (app.go).
  if (mode === "fresh") {
    try {
      if (!(await api.ConfirmErase(d))) return;
    } catch (e) {
      ui.error = errText(e);
      show("error");
      return;
    }
  }
  ui.progress = null;
  ui.log = [];
  ui.steps = await api.Steps();
  show("run");
  try {
    ui.result = (await api.Prepare(d, mode)) || { ejected: false, ejectError: "" };
    show("done");
  } catch (e) {
    ui.error = errText(e);
    ui.log.push("Error: " + ui.error);
    show("error");
  }
}

document.addEventListener("click", (ev) => {
  const t = ev.target.closest("[data-do]");
  if (!t || t.disabled) return;
  const d = t.dataset.do;
  if (d === "look") look(t.dataset.v);
  else if (d === "download") getISO("download");
  else if (d === "choose") getISO("choose");
  else if (d === "cancel") api.Cancel();
  else if (d === "showall") { ui.showAll = !ui.showAll; ui.sel = ""; ui.drives = null; refreshDrives(); render(); }
  else if (d === "back") show(ui.view === "confirm" ? "drives" : "release");
  else if (d === "next") { const s = selected(); ui.mode = s && s.juneStick ? "update" : "fresh"; ui.ack = false; show("confirm"); }
  else if (d === "write") write();
  else if (d === "guide") api.OpenGuide();
  else if (d === "again") { ui.sel = ""; ui.drives = null; show(ui.iso ? "drives" : "release"); }
});

document.addEventListener("change", (ev) => {
  const t = ev.target;
  if (t.name === "drive") ui.sel = t.value;
  else if (t.name === "release") { ui.pick = +t.value; ui.release = ui.releases[ui.pick]; ui.olderOpen = true; }
  else if (t.name === "mode") { ui.mode = t.value; ui.ack = false; }
  else if (t.name === "ack") ui.ack = t.checked;
  else return;
  render();
});

document.addEventListener("toggle", (ev) => {
  if (ev.target.id === "r-log") ui.logOpen = ev.target.open;
  else if (ev.target.classList.contains("older")) ui.olderOpen = ev.target.open;
}, true);

(async () => {
  look(savedLook(), false);
  try {
    await connect();
    look(savedLook(), false); // now the runtime is there for the title bar
  } catch (e) {
    ui.error = errText(e);
    show("error");
    return;
  }
  on("bytes", (b) => {
    if (!ui.bytes) return;
    if (ui.bytes.phase !== b.phase) ui.bytes.t0 = Date.now();
    Object.assign(ui.bytes, b);
    if (ui.view === "release") render();
  });
  on("progress", (p) => { ui.progress = p; if (ui.view === "run") updateRun(); });
  on("log", (l) => { ui.log.push(l); if (ui.view === "run") updateRun(); });
  try { ui.info = Object.assign(ui.info, await api.Info()); } catch (e) { /* keep defaults */ }
  render();
  await loadRelease();
  // Preview only: "?mock&screen=drives" opens a screen directly, for docs screenshots.
  const screen = PREVIEW && new URLSearchParams(location.search).get("screen");
  if (screen === "drives") {
    ui.iso = { version: "v0.5", verified: true, bios: true };
    ui.sel = "3";
    show("drives");
  }
})();

/* ---- Mock backend (browser preview only) ------------------------------- */

function mockApi() {
  const handlers = {};
  window.__mockHandlers = handlers;
  const emit = (n, v) => (handlers[n] || []).forEach((f) => f(v));
  const wait = (ms) => new Promise((r) => setTimeout(r, ms));
  const drives = [
    { id: "3", name: "SanDisk Ultra USB 3.0", size: 30752636928, bus: "USB", eligible: true, juneStick: false,
      volumes: [{ label: "PHOTOS", fs: "FAT32", mount: "E:\\", size: 30752636928 }] },
    { id: "4", name: "Kingston DataTraveler 3.0", size: 15502147584, bus: "USB", eligible: true, juneStick: true,
      volumes: [{ label: "JUNEBOOT", fs: "FAT32", mount: "F:\\" }, { label: "JUNE", fs: "exFAT", mount: "G:\\" }] },
    { id: "5", name: "Generic Flash Disk", size: 7998537728, bus: "USB", eligible: false, reason: "Too small: at least 16 GB is needed (this drive has 8.0 GB)", volumes: [] },
  ];
  const steps = ["Prepare the stick", "Copy the installer", "Verify the copy", "Make it bootable", "Finish"];
  let cancelled = false;
  return {
    // "#arm" previews the note an ARM computer shows.
    Info: async () => ({ version: "1.2", os: "windows", guideUrl: "https://project-july.org/setup", arm: location.hash === "#arm" }),
    Release: async () => { await wait(400); return { version: "v0.9.0", url: "", sha256: "1db073270cadf6d8cd9f2b49480882ccd3dea2212f28ae8055f1698314ccbe98", size: 1416314880 }; },
    // "#one" leaves Releases out, to preview the latest.json-only screen.
    Releases: location.hash === "#one" ? undefined : async () => {
      await wait(400);
      return [
        { version: "v0.9.0", sha256: "1db073270cadf6d8cd9f2b49480882ccd3dea2212f28ae8055f1698314ccbe98", size: 1416314880, date: "2026-10-24" },
        { version: "v0.8.1", sha256: "8bb37bc681c61ba5d45ead47d46645f8795a3640db4b03b8a33533115a8e035e", size: 1413529600, date: "2026-10-24" },
        { version: "v0.8", sha256: "19378a2b9d75339d82655c8cf032b2d5caead5da71968563a5d94f7a45f824f3", size: 1413529600, date: "2026-10-24" },
        { version: "v0.7", sha256: "ac0a86673b7cc7c15d73f7109066baf1a43f7f1f1767555b20d2dacef59438c5", size: 1413529600, date: "2026-10-11" },
        { version: "v0.6", sha256: "89a7d6b9d309241a4514b21a57ac9d49ea3f02853136193a26570613ff3d0608", size: 1413529600, date: "2026-10-11" },
        { version: "v0.5", sha256: "2d768b23c47cc68e71271d97564820702df78415cf56796ee71d22048e1e309a", size: 1406533632, date: "2026-10-10" },
      ];
    },
    Download: async (version) => {
      cancelled = false;
      for (let d = 0; d <= 1406533632; d += 60e6) {
        if (cancelled) throw new Error("cancelled");
        emit("bytes", { done: d, total: 1406533632, phase: "download" });
        await wait(80);
      }
      return { path: "june-local-live.iso", version: version || "v0.9.0", sha256: "", size: 1416314880, verified: true, bios: true };
    },
    ChooseISO: async () => ({ path: "x.iso", version: "build 20261005-09:29", sha256: "", size: 1405157376, verified: false, bios: false }),
    Cancel: () => { cancelled = true; },
    Drives: async () => (location.hash === "#empty" ? [] : drives),
    Steps: async () => steps,
    ConfirmErase: async (d) => window.confirm(`Erase ${stickName(d)} (${d.name})?`),
    Prepare: async (d) => {
      const fail = location.hash === "#fail";
      for (let s = 0; s < steps.length; s++) {
        emit("log", steps[s]);
        for (let f = 0; f <= 1; f += 0.1) {
          emit("progress", { step: s, fraction: f, overall: (s + f) / steps.length, detail: s === 1 ? "live/filesystem.squashfs" : "" });
          await wait(120);
        }
        if (fail && s === 1) throw new Error("writing live/filesystem.squashfs to the stick: the device is not ready");
      }
      return location.hash === "#noeject"
        ? { ejected: false, ejectError: "Windows could not eject it because a program is using it (explorer.exe)" }
        : { ejected: true, ejectError: "" };
    },
    OpenGuide: () => window.open("https://project-july.org/setup"),
  };
}

function mockOn(name, fn) {
  (window.__mockHandlers[name] = window.__mockHandlers[name] || []).push(fn);
}
})();
