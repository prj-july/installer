/* June Local installer UI.  Plain script, no build step, no network access
   beyond the installer itself.  Server state arrives over /api/events; the
   wizard steps before a job starts live only in this page. */
"use strict";

const $ = (sel, root = document) => root.querySelector(sel);
const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

/* ---- Icons and status marks ------------------------------------------- */

const MARK = {
  wait: '<svg class="mark m-wait" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" stroke-dasharray="3 3.1"/></svg>',
  ok: '<svg class="mark m-ok" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M8 12.4l2.8 2.8L16.2 9.6"/></svg>',
  done: '<svg class="mark m-ok solid" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="10"/><path d="M7.6 12.4l3 3 5.8-5.8" stroke-width="2.6"/></svg>',
  run: '<svg class="mark m-run" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" opacity=".25"/><path class="spin" d="M12 3a9 9 0 0 1 9 9"/></svg>',
  warn: '<svg class="mark m-warn" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3.6 2.8 19.6h18.4z"/><path d="M12 10v4.2M12 17.1v.01"/></svg>',
};
const CHECK = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';
const CHEVRON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 6l6 6-6 6"/></svg>';
const CLOSE = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18"/></svg>';
// Tile icons: the Project July website's icon set (sprite in index.html).
const icon = (id, cls = "") => `<svg${cls ? ` class="${cls}"` : ""} viewBox="0 0 24 24" aria-hidden="true"><use href="#i-${id}"/></svg>`;
const GLYPH = {
  // The June oven itself: the Project July mark, not the generic range icon.
  install: '<svg class="glyph glyph-mark" viewBox="0 0 64 64" aria-hidden="true"><use href="#i-mark"/></svg>',
  custom: icon("key", "glyph"),
  // Oven -> USB stick and back: what the job actually does.
  backup: '<svg class="glyph wide" viewBox="0 0 48 24" aria-hidden="true"><use href="#i-backup"/></svg>',
  restore: '<svg class="glyph wide" viewBox="0 0 48 24" aria-hidden="true"><use href="#i-restore"/></svg>',
  more: icon("sliders", "glyph"),
  phone: icon("phone", "glyph"),
};

/* ---- Copy --------------------------------------------------------------- */

const ACTIONS = {
  install: {
    name: "Install June Local", tag: "Recommended",
    sub: "Keeps June's certificate and the factory app. The quickest way.",
    steps: ["Options", "Connect oven", "Install"], go: "Install June Local",
  },
  custom: {
    name: "Install with your own CA",
    sub: "Advanced. Creates a certificate authority just for your home.",
    steps: ["Details", "Connect oven", "Install"], go: "Install June Local",
  },
  backup: {
    name: "Back up the oven",
    sub: "A full copy of the oven's storage on a USB drive. 15 to 30 minutes.",
    steps: ["Drive", "Connect oven", "Back up"], go: "Start backup",
  },
  restore: {
    name: "Restore a backup",
    sub: "Puts a backup image back on the oven.",
    steps: ["Backup", "Connect oven", "Restore"], go: "Erase oven and restore",
  },
};

/* ---- State -------------------------------------------------------------- */

let S = null; // last server state
const ui = {
  view: "home", action: null, form: {}, dests: null, destErr: "", images: null,
  error: "", busy: false, jobKey: "", logOpen: false, armed: "",
};

/* ---- Helpers ------------------------------------------------------------ */

const gb = (b) => (b / 1e9).toFixed(b >= 100e9 ? 0 : 1) + " GB";
const gib = (b) => (b / 2 ** 30).toFixed(1);
function clock(sec) {
  sec = Math.max(0, Math.floor(sec));
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return h ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}
const left = (sec) => (sec >= 90 ? `${Math.round(sec / 60)} min` : `${sec} s`);
function defaultFilename() {
  const d = new Date(), p = (n) => String(n).padStart(2, "0");
  return `${String(d.getFullYear()).slice(2)}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}.img`;
}

async function api(path, body) {
  const opt = body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  const res = await fetch(path, opt);
  let data = null;
  try { data = await res.json(); } catch (_) { /* empty */ }
  if (!res.ok) throw new Error((data && data.error) || `Request failed (${res.status}).`);
  return data;
}

let toastTimer = 0;
function toast(msg) {
  const t = $("#toast");
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), 4000);
}

/* ---- Header ------------------------------------------------------------- */

function renderHeader() {
  if (!S) return;
  $("#brand-sub").textContent = S.local ? `June Local installer · build ${S.build}` : "June Local installer · on your phone";
  const chip = $("#oven-chip");
  const job = S.job && S.job.status === "running";
  let html;
  if (job) html = `${MARK.run}<span>Working</span>`;
  else if (S.oven.mode === "apx") html = `${MARK.ok}<span>Oven · RCM mode</span>`;
  else if (S.oven.mode === "ums") html = `${MARK.ok}<span>Oven · storage open</span>`;
  else html = `${MARK.wait}<span>No oven</span>`;
  if (chip.innerHTML !== html) chip.innerHTML = html;

  const pb = $("#phone-btn");
  pb.hidden = !S.local;
  pb.classList.toggle("on", S.net.remote);
  const badge = $("#phone-badge");
  badge.hidden = !S.net.clients;
  badge.textContent = S.net.clients;
  if (sheetOpen === "phone") renderPhoneSheet();
}

/* ---- Views -------------------------------------------------------------- */

function setView(view, extra) {
  Object.assign(ui, { view, error: "" }, extra || {});
  render();
  window.scrollTo(0, 0);
}

function render() {
  renderHeader();
  const main = $("#main");
  if (!S) return;
  if (S.job) {
    renderJob(main);
    return;
  }
  ui.jobKey = "";
  const html = ui.view === "home" ? viewHome()
    : ui.view === "options" ? viewOptions()
    : ui.view === "connect" ? viewConnect() : viewHome();
  // Fade in only when the screen changes, not when an option re-renders it.
  const screen = `${ui.view}:${ui.action}`;
  const enter = ui.screen !== screen ? " enter" : "";
  ui.screen = screen;
  main.innerHTML = `<div class="view${enter}">${html}</div>${devPanel()}`;
  bindView(main);
}

/* Partial refresh on every server update: never rebuild inputs. */
function refresh() {
  renderHeader();
  if (S.job) return renderJob($("#main"));
  if (ui.jobKey) return render(); // job was dismissed elsewhere
  if (ui.view === "connect") updateDetect();
  if (ui.view === "home") {
    const hs = $("#home-status");
    if (hs) hs.innerHTML = homeStatus();
  }
  const dev = $("#dev");
  if (dev) dev.outerHTML = devPanel();
  bindDev();
}

function homeStatus() {
  if (S.oven.present) return `${MARK.ok}<span>Oven connected · ${S.oven.mode === "apx" ? "RCM mode" : "storage open"}</span>`;
  return `${MARK.wait}<span>No oven connected yet</span>`;
}

function viewHome() {
  const tiles = Object.entries(ACTIONS).map(([key, a]) => `
    <button class="tile" type="button" data-act="${key}">
      ${GLYPH[key]}
      ${a.tag ? `<span class="tag">${esc(a.tag)}</span>` : ""}
      <span class="tile-name">${esc(a.name)}</span>
      <span class="tile-sub">${esc(a.sub)}</span>
    </button>`).join("");
  const extra = S.local
    ? `<button class="tile grey" type="button" data-sheet="more">${GLYPH.more}<span class="tile-name">More</span><span class="tile-sub">Setup guide, text mode, restart</span></button>`
    : `<button class="tile grey" type="button" data-sheet="files">${GLYPH.phone}<span class="tile-name">Phone setup</span><span class="tile-sub">Get the companion app on this phone</span></button>`;
  return `
    <div class="home-head">
      <div>
        <p class="eyebrow">June Local</p>
        <h1>What would you like to do?</h1>
      </div>
      <div class="home-status" id="home-status">${homeStatus()}</div>
    </div>
    <div class="tiles five">${tiles}${extra}</div>
    ${S.local ? `<div class="home-foot">
      <p class="muted small" style="margin:0">Each job asks you to put the oven in recovery mode first. Nothing changes until you press the final button.</p>
      <button class="btn ghost" type="button" data-sheet="phone">${phoneIcon()}Use your phone instead</button>
    </div>` : ""}`;
}

function phoneIcon() {
  return icon("phone");
}

function wizard(stepIdx) {
  const a = ACTIONS[ui.action];
  return `<ol class="wizard" aria-label="Steps">${a.steps.map((label, i) => `
    <li class="${i < stepIdx ? "done" : i === stepIdx ? "on" : ""}"${i === stepIdx ? ' aria-current="step"' : ""}>
      <span class="n">${i < stepIdx ? CHECK.replace("<svg", '<svg style="width:14px;height:14px;fill:none;stroke:currentColor;stroke-width:3;stroke-linecap:round;stroke-linejoin:round"') : i + 1}</span><span>${esc(label)}</span>
    </li>`).join("")}</ol>`;
}

function opt(name, title, sub, checked, { radio = false, value = "", side = "" } = {}) {
  return `<label class="opt${checked ? " sel" : ""}">
    <input type="${radio ? "radio" : "checkbox"}" name="${name}" value="${esc(value)}"${checked ? " checked" : ""}>
    <span class="box${radio ? " round" : ""}">${CHECK}</span>
    <span class="opt-main"><span class="opt-title">${title}</span>${sub ? `<span class="opt-sub">${sub}</span>` : ""}</span>
    ${side ? `<span class="opt-side">${side}</span>` : ""}
  </label>`;
}

function errLine() {
  return ui.error ? `<div class="err" role="alert">${MARK.warn}<span>${esc(ui.error)}</span></div>` : "";
}

function viewOptions() {
  const a = ACTIONS[ui.action], f = ui.form;
  let body = "", canNext = true;
  if (ui.action === "install") {
    body = `
      <h2>Install June Local on this oven</h2>
      <p class="muted">The oven keeps June's certificate and its factory messaging app, so the June app works as before, but talks to June Local instead of June's cloud.</p>
      <div class="opts">
        ${opt("persist", "Keep the no-pin patch after a factory reset", "Off by default. Only needed if you plan to switch this oven to your own CA later.", f.persist)}
      </div>`;
  } else if (ui.action === "custom") {
    body = `
      <h2>Your oven's address</h2>
      <p class="muted">A private certificate authority is created on this USB stick. It signs a certificate for your oven's address, and you install the CA on your phone afterwards.</p>
      <div class="fields">
        <div class="field"><label for="f-ip">Oven LAN IP</label><input id="f-ip" name="ip" inputmode="decimal" autocomplete="off" placeholder="192.168.1.50" value="${esc(f.ip || "")}"><div class="hint">Reserve this address for the oven in your router.</div></div>
        <div class="field"><label for="f-host">Hostname</label><input id="f-host" name="host" autocomplete="off" spellcheck="false" value="${esc(f.host == null ? "oven.local" : f.host)}"></div>
      </div>
      <div class="opts">
        ${opt("persist", "Keep the patch after a factory reset", "Stores the patched messaging app in /system so a factory reset doesn't remove it.", f.persist)}
      </div>`;
    canNext = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/.test(f.ip || "");
  } else {
    const restore = ui.action === "restore";
    let list;
    if (ui.dests === null) list = `<div class="opts"><div class="skel"></div><div class="skel"></div></div>`;
    else if (!ui.dests.length) list = `<div class="warnline">${MARK.warn}<span>No usable drive found. Plug in an exFAT, NTFS or ext4 USB drive${restore ? "" : " with at least 8 GB free"}, then rescan.</span></div>`;
    else list = `<div class="opts">${ui.dests.map((d) => opt("dest", esc(d.label) + `<span class="pill">${d.origin === "boot" ? "Boot USB" : d.origin === "usb" ? "USB" : "Internal"}</span>`, esc(d.tag), f.dest === d.tag, { radio: true, value: d.tag, side: d.free ? `${gb(d.free)} free` : "" })).join("")}</div>`;
    body = `
      <h2>${restore ? "Where is the backup?" : "Where should the backup go?"}</h2>
      <p class="muted">${restore ? "Pick the drive that holds the backup image." : "The backup is one 7.8 GB file."} This computer runs from memory, so you can unplug the boot stick and use another drive.</p>
      ${list}
      ${ui.destErr ? `<div class="err">${MARK.warn}<span>${esc(ui.destErr)}</span></div>` : ""}
      <div class="row"><button class="btn ghost" type="button" data-do="rescan">Rescan drives</button></div>
      <div class="fields">
        <div class="field${restore ? " wide" : ""}"><label for="f-folder">Folder (optional)</label><input id="f-folder" name="folder" autocomplete="off" spellcheck="false" placeholder="${restore ? "Leave empty for the top level" : "june-backups"}" value="${esc(f.folder || "")}"></div>
        ${restore ? "" : `<div class="field"><label for="f-file">File name</label><input id="f-file" name="filename" autocomplete="off" spellcheck="false" value="${esc(f.filename || defaultFilename())}"></div>`}
      </div>`;
    if (restore) {
      let imgs = "";
      if (ui.images === "loading") imgs = `<div class="opts"><div class="skel"></div></div>`;
      else if (Array.isArray(ui.images)) {
        imgs = ui.images.length
          ? `<div class="opts">${ui.images.map((im) => opt("image", esc(im.name) + (im.ok ? "" : '<span class="pill warn">Wrong size</span>'), im.ok ? "Complete oven image" : `${gb(im.size)}, a full image is 7.8 GB`, f.image === im.name, { radio: true, value: im.name, side: gb(im.size) })).join("")}</div>`
          : `<div class="warnline">${MARK.warn}<span>No .img backups in this folder.</span></div>`;
      }
      body += `
        <div class="row"><button class="btn ghost" type="button" data-do="find"${f.dest ? "" : " disabled"}>Find backups</button></div>
        ${imgs}
        ${f.image ? `
          <div class="warnline">${MARK.warn}<span>Restoring replaces everything on the oven with <b>${esc(f.image)}</b>. June Local and any changes since the backup are removed.</span></div>
          <div class="fields"><div class="field"><label for="f-confirm">Type RESTORE to confirm</label><input id="f-confirm" name="confirm" autocomplete="off" spellcheck="false" autocapitalize="characters" value="${esc(f.confirm || "")}"></div></div>` : ""}`;
      canNext = !!(f.dest && f.image && f.confirm === "RESTORE");
    } else {
      canNext = !!f.dest;
    }
  }
  return `
    <div class="flow">
      ${wizard(0)}
      <div>
        <p class="eyebrow">${esc(a.name)}</p>
        ${body}
        ${errLine()}
        <div class="actions">
          <button class="btn" type="button" data-do="next" id="next"${canNext ? "" : " disabled"}>Next: connect the oven ${CHEVRON}</button>
          <button class="btn ghost" type="button" data-do="home">Back</button>
        </div>
      </div>
    </div>`;
}

const ILLUSTRATION = `
<svg viewBox="0 0 520 300" role="img" aria-label="USB cable from this computer to the oven board; hold SW2 while switching the oven on">
  <style>
    .il-s{fill:none;stroke:var(--fg2);stroke-width:2;stroke-linecap:round;stroke-linejoin:round}
    .il-t{font:600 12px var(--f);letter-spacing:.08em;fill:var(--fg2)}
    .il-n{font:700 13px var(--f);fill:#160a04}
    .il-e{fill:none;stroke:var(--ember);stroke-width:2.4;stroke-linecap:round}
  </style>
  <rect class="il-s" x="22" y="62" width="150" height="98" rx="8"/>
  <path class="il-s" d="M8 176h178l-12-16H20z"/>
  <rect class="il-s" x="186" y="140" width="10" height="14" rx="2"/>
  <path class="il-e" d="M196 147c60 0 70 70 120 70h8" stroke-dasharray="6 6"><animate attributeName="stroke-dashoffset" from="24" to="0" dur="1s" repeatCount="indefinite"/></path>
  <rect class="il-s" x="300" y="34" width="196" height="232" rx="14"/>
  <text class="il-t" x="318" y="60">OVEN BOARD</text>
  <rect class="il-s" x="314" y="208" width="26" height="18" rx="3"/>
  <text class="il-t" x="306" y="246">USB</text>
  <circle cx="430" cy="118" r="17" fill="none" stroke="var(--ember)" stroke-width="2" opacity=".35"><animate attributeName="r" values="17;34" dur="1.8s" repeatCount="indefinite"/><animate attributeName="opacity" values=".5;0" dur="1.8s" repeatCount="indefinite"/></circle>
  <circle cx="430" cy="118" r="14" fill="var(--ember)" style="filter:drop-shadow(0 0 8px var(--glow))"/>
  <text class="il-t" x="414" y="160">SW2</text>
  <rect class="il-s" x="424" y="206" width="48" height="24" rx="12"/>
  <circle cx="460" cy="218" r="8" fill="var(--fg2)"/>
  <text class="il-t" x="420" y="250">POWER</text>
  <g><circle cx="248" cy="166" r="12" fill="var(--ember2)"/><text class="il-n" x="244" y="171">1</text></g>
  <g><circle cx="388" cy="96" r="12" fill="var(--ember2)"/><text class="il-n" x="384" y="101">2</text></g>
  <g><circle cx="402" cy="218" r="12" fill="var(--ember2)"/><text class="il-n" x="398" y="223">3</text></g>
</svg>`;

function detectHTML() {
  const o = S.oven;
  if (o.present) {
    return `<div class="detect found" id="detect">${MARK.ok}<div><div class="detect-word">Oven found · ${o.mode === "apx" ? "RCM mode" : "storage open"}</div><div class="detect-sub">${o.mode === "apx" ? "You can let go of SW2." : "The oven's storage is already open."}</div></div></div>`;
  }
  return `<div class="detect" id="detect">${MARK.run}<div><div class="detect-word">Waiting for the oven</div><div class="detect-sub">Looking for it on USB. This updates by itself.</div></div></div>`;
}

function updateDetect() {
  const d = $("#detect");
  if (!d) return;
  const html = detectHTML();
  if (d.outerHTML !== html) d.outerHTML = html;
  const go = $("#go");
  if (go) go.disabled = !S.oven.present || ui.busy;
}

function viewConnect() {
  const a = ACTIONS[ui.action];
  const danger = ui.action === "restore";
  return `
    <div class="flow">
      ${wizard(1)}
      <div>
        <p class="eyebrow">${esc(a.name)}</p>
        <h2>Put the oven in recovery mode</h2>
        <div class="connect">
          <div class="illus">${ILLUSTRATION}</div>
          <div>
            <ol class="steps">
              <li><span>Connect the oven to this computer with the USB cable. Use a port on the computer itself, not a hub.</span></li>
              <li><span>Press and hold <b>SW2</b> (force recovery) on the oven's board.</span></li>
              <li><span>Switch the oven's power on and keep holding SW2 for about 10 seconds.</span></li>
            </ol>
            ${detectHTML()}
          </div>
        </div>
        ${errLine()}
        <div class="actions">
          <button class="btn${danger ? " danger" : ""}" type="button" id="go" data-do="go"${S.oven.present && !ui.busy ? "" : " disabled"}>${esc(a.go)}</button>
          <button class="btn ghost" type="button" data-do="back">Back</button>
          <button class="link" type="button" data-do="go-anyway">Start without detecting</button>
        </div>
        <p class="muted small" style="margin-top:14px">Once it starts, the oven's fans spin up and stay fast while it works. That is normal.</p>
      </div>
    </div>`;
}

/* ---- Running job and result -------------------------------------------- */

function renderJob(main) {
  const j = S.job;
  const key = `${j.id}:${j.status}`;
  if (ui.jobKey !== key) {
    ui.jobKey = key;
    if (j.status === "failed") ui.logOpen = true;
    ui.screen = "";
    main.innerHTML = `<div class="view enter">${j.status === "running" ? jobRunningShell(j) : jobResult(j)}</div>${devPanel()}`;
    bindView(main);
  }
  updateJob(j);
}

function jobRunningShell(j) {
  return `
    <div class="run">
      <div>
        <p class="eyebrow">${esc(j.title)}</p>
        <div class="hero" id="j-hero"></div>
        <div class="st" id="j-st"></div>
        <div class="bar" id="j-bar"><i></i></div>
        <div class="facts" id="j-facts"></div>
        <div class="keep">${MARK.warn}<span>Keep the oven connected and powered. Don't unplug anything.</span></div>
        <div class="actions" id="j-actions"></div>
        ${logDetails()}
      </div>
      <ol class="tracker" id="j-tracker" aria-label="Progress"></ol>
    </div>`;
}

function logDetails() {
  return `<details class="log" id="j-log"${ui.logOpen ? " open" : ""}><summary>${CHEVRON}Show details</summary><div class="logbox" id="j-logbox"></div></details>`;
}

function trackerHTML(j) {
  return j.steps.map((s, i) => {
    let cls = "", n = "";
    if (i < j.step) { cls = "done"; n = CHECK; }
    else if (i === j.step && j.status === "running") cls = "on";
    else if (i === j.step && j.status === "failed") { cls = "fail"; n = "!"; }
    return `<li class="${cls}"${cls === "on" ? ' aria-current="step"' : ""}><span class="n">${n}</span><span>${esc(s.label)}</span></li>`;
  }).join("");
}

function updateJob(j) {
  const set = (id, html) => { const el = document.getElementById(id); if (el && el.innerHTML !== html) el.innerHTML = html; };
  const p = j.progress;
  if (j.status === "running") {
    const stepLabel = (j.steps[j.step] || {}).label || "Working";
    if (p && p.pct > 0) set("j-hero", `${Math.floor(p.pct)}<small>%</small>`);
    else set("j-hero", clock(j.elapsed));
    // The hero already shows elapsed time when there is no percentage.
    const q = p && p.eta != null ? `· ${left(p.eta)} left` : p ? "· estimating" : "· elapsed";
    set("j-st", `${MARK.run}<span>${esc(stepLabel)}</span><span class="q">${esc(q)}</span>`);
    const bar = $("#j-bar");
    if (bar) {
      bar.className = p ? "bar" : "bar ind";
      bar.style.setProperty("--p", p ? (p.pct / 100).toFixed(4) : "0");
    }
    let facts = j.stage ? esc(j.stage) : "Starting…";
    if (p) facts = `<b>${gib(p.bytes)} of ${gib(p.total)} GiB</b>${p.rate ? ` at ${p.rate} MB/s` : ""}<br>${facts}`;
    set("j-facts", facts);
    set("j-actions", j.cancellable ? `<button class="btn ghost" type="button" data-do="cancel">Stop waiting</button>` : "");
  }
  set("j-tracker", trackerHTML(j));
  const box = $("#j-logbox");
  if (box) {
    const atEnd = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
    const html = j.log.map((l) => {
      const cls = /^\[-\]|^\[!\]/.test(l) ? "l-bad" : /^\[\+\]|SUCCESS/.test(l) ? "l-ok" : /^\[\*\]/.test(l) ? "l-st" : "";
      return cls ? `<span class="${cls}">${esc(l)}</span>` : esc(l);
    }).join("\n");
    if (box.innerHTML !== html) {
      box.innerHTML = html;
      if (atEnd) box.scrollTop = box.scrollHeight;
    }
  }
}

function downloadsHTML(j) {
  const files = [];
  if (j.action === "custom" && S.files["ca.crt"]) files.push(["ca.crt", "Your home's CA certificate", "Install it on the phone: Settings › Security › Install a certificate › CA certificate.", "your CA certificate"]);
  if (S.files["companion.apk"]) files.push(["companion.apk", "Companion app", "Install it, then set the oven's address as the endpoint.", "the companion app"]);
  if (!files.length) return "";
  if (!S.local) {
    return `<div class="dl">${files.map(([f, t, s]) => `<div class="dl-card"><div><div class="opt-title">${t}</div><div class="opt-sub">${s}</div><div class="row" style="margin-top:12px"><a class="btn" href="/files/${f}">Download</a></div></div></div>`).join("")}</div>`;
  }
  if (!S.net.remote) {
    return `<div class="dl"><div class="dl-card"><div><div class="opt-title">Get the phone files</div><div class="opt-sub">Turn on phone access to show a code for ${files.map((f) => f[3]).join(" and ")}.</div><div class="row" style="margin-top:12px"><button class="btn" type="button" data-sheet="phone">Connect a phone</button></div></div></div></div>`;
  }
  return `<div class="dl">${files.map(([f, t, s]) => `<div class="dl-card"><div class="qr"><img alt="QR code for ${esc(t)}" src="/api/qr?for=${f}&amp;r=${Date.now()}"></div><div><div class="opt-title">${t}</div><div class="opt-sub">Scan with the phone. ${s}</div></div></div>`).join("")}</div>`;
}

function jobResult(j) {
  const ok = j.status === "success";
  const word = ok ? "Done" : j.status === "cancelled" ? "Stopped" : "Did not finish";
  const heroText = ok ? { install: "June Local is installed", custom: "June Local is installed", backup: "Backup saved", restore: "Oven restored" }[j.action]
    : j.status === "cancelled" ? "Stopped before anything changed" : "Something went wrong";
  let body = "";
  if (ok && (j.action === "install" || j.action === "custom")) {
    body = `
      <ol class="result-list">
        <li>Unplug the oven's power and the USB cable.</li>
        <li>Wait 10 seconds, plug the power back in and let the oven start normally.</li>
        <li>From a device on the same network, open <b>http://&lt;oven-ip&gt;/local</b>. You should see the June Local status page.</li>
        <li>Pair from the June app or Home Assistant. Then close up the oven.</li>
      </ol>
      ${j.params.persist ? `<p class="muted small" style="margin-top:12px">The no-pin patch is staged in /system and is applied on the oven's next start.</p>` : ""}
      ${downloadsHTML(j)}`;
  } else if (ok && j.action === "backup") {
    body = `<p class="muted">Saved as <b>${esc([j.params.folder, j.params.filename].filter(Boolean).join("/"))}</b>, with a .sha256 file next to it. Safely unplug the drive, then unplug the oven's power and USB cable.</p>`;
  } else if (ok) {
    body = `<p class="muted">Unplug the oven's power and the USB cable, close up the oven, then switch it on normally.</p>`;
  } else {
    const reasons = j.reasons.length ? j.reasons : [j.status === "cancelled" ? "You stopped the job while it was waiting for the oven." : "The worker stopped without saying why. The details below show its last steps."];
    body = `<ul class="result-list">${reasons.map((r) => `<li>${esc(r)}</li>`).join("")}</ul>`;
  }
  const saved = j.saved_to ? `<p class="muted small" style="margin-top:16px">Log saved to ${esc(j.saved_to)}</p>` : "";
  return `
    <div class="run">
      <div>
        <p class="eyebrow">${esc(j.title)}</p>
        <div class="hero is-text">${esc(heroText)}</div>
        <div class="st">${ok ? MARK.done : j.status === "cancelled" ? MARK.wait : MARK.warn}<span>${word}</span><span class="q">· ${clock(j.elapsed)}</span></div>
        <div class="bar ${ok ? "ok" : "bad"}"><i></i></div>
        ${body}
        ${saved}
        <div class="actions">
          ${ok ? `<button class="btn" type="button" data-do="dismiss">Done</button>`
               : `<button class="btn" type="button" data-do="retry">Try again</button><button class="btn ghost" type="button" data-do="dismiss">Back to start</button>`}
        </div>
        ${logDetails()}
      </div>
      <ol class="tracker" id="j-tracker" aria-label="Progress"></ol>
    </div>`;
}

/* ---- Sheets: phone pairing, more, phone files -------------------------- */

let sheetOpen = "";

function openSheet(name) {
  sheetOpen = name;
  const d = $("#sheet");
  if (name === "phone") renderPhoneSheet();
  else if (name === "more") renderMoreSheet();
  else if (name === "files") renderFilesSheet();
  if (!d.open) d.showModal();
}

function sheetHead(title) {
  return `<div class="sheet-head"><h2>${title}</h2><button class="close" type="button" data-do="close-sheet" aria-label="Close">${CLOSE}</button></div>`;
}

function renderPhoneSheet() {
  const n = S.net;
  const ip = n.ips[0];
  let body;
  if (!n.ips.length) {
    body = `<div class="warnline">${MARK.warn}<span>This computer is not on a network. Plug in an Ethernet cable from your router, then try again.</span></div>`;
  } else if (!n.remote) {
    body = `<p class="muted">Your phone shows the same screens and can start and follow jobs, and it can download the companion app. It has to be on the same network as this computer (${esc(ip)}).</p>`;
  } else {
    body = `
      <div class="pair">
        <div class="qr" id="pair-qr" data-url="${esc(n.url)}"><img alt="QR code to open the installer on your phone" src="/api/qr?for=phone&amp;r=${encodeURIComponent(n.url)}" onerror="this.parentNode.innerHTML='<p style=&quot;color:#000;font:500 14px var(--f);margin:6px&quot;>Install qrencode to show a code.</p>'"></div>
        <div>
          <p class="muted small" style="margin-bottom:6px">Scan with your phone's camera, or type:</p>
          <div class="pair-url">${esc(n.url.replace(/^http:\/\//, ""))}</div>
          <p class="small" style="margin-top:14px">${n.clients ? `${MARK.ok.replace('class="mark', 'style="width:18px;height:18px;display:inline-block;vertical-align:-3px" class="mark')} ${n.clients} phone${n.clients > 1 ? "s" : ""} connected` : '<span class="muted">No phone connected yet</span>'}</p>
        </div>
      </div>`;
  }
  const html = `<div class="sheet-in">
    ${sheetHead("Connect a phone")}
    ${body}
    ${n.ips.length ? `<button class="switch" type="button" role="switch" aria-checked="${n.remote}" data-do="toggle-remote">
      <span><span class="opt-title">Allow a phone on this network</span><span class="opt-sub" style="display:block">Anyone who scans the code can control the installer. Turning it off disconnects phones and makes a new code.</span></span>
      <span class="track"></span></button>` : ""}
  </div>`;
  const d = $("#sheet");
  // Keep the QR image element when nothing relevant changed (no flicker).
  const sig = `${n.remote}|${n.url}|${n.clients}|${n.ips.join(",")}`;
  if (d.dataset.sig !== sig) {
    d.dataset.sig = sig;
    d.innerHTML = html;
  }
}

function renderMoreSheet() {
  const armed = (k, label, armedLabel, cls) => `<button class="btn ${ui.armed === k ? "danger" : cls}" type="button" data-do="${k}">${ui.armed === k ? armedLabel : label}</button>`;
  $("#sheet").dataset.sig = "";
  $("#sheet").innerHTML = `<div class="sheet-in">
    ${sheetHead("More")}
    <div class="pair" style="margin-top:6px">
      <div class="qr" style="width:132px;height:132px"><img alt="QR code for the setup guide" src="/api/qr?for=guide" onerror="this.parentNode.hidden=true"></div>
      <div><div class="opt-title">Setup guide</div><p class="muted small">Photos of the board, the SW2 button and the USB port: <b>${esc(S.guide.replace(/^https?:\/\//, ""))}</b></p></div>
    </div>
    <div class="menu">
      <button class="btn ghost" type="button" data-do="text-mode">Switch to the text installer</button>
      ${armed("reboot", "Restart this computer", "Tap again to restart", "ghost")}
      ${armed("poweroff", "Shut down", "Tap again to shut down", "ghost")}
    </div>
    <p class="muted small" style="margin-top:16px">Remove the USB stick when the computer restarts so it starts its normal system again.</p>
  </div>`;
}

function renderFilesSheet() {
  const f = S.files;
  $("#sheet").dataset.sig = "";
  $("#sheet").innerHTML = `<div class="sheet-in">
    ${sheetHead("Phone setup")}
    <div class="menu">
      ${f["companion.apk"] ? `<a class="btn" href="/files/companion.apk">Download the companion app</a>` : `<p class="muted">The companion app is not on this installer.</p>`}
      ${f["ca.crt"] ? `<a class="btn ghost" href="/files/ca.crt">Download your home's CA certificate</a>` : ""}
    </div>
    <p class="muted small" style="margin-top:16px">After installing the app, set the endpoint to your oven's address. If you used your own CA, install the certificate under Settings › Security › Install a certificate › CA certificate.</p>
  </div>`;
}

/* ---- Dev panel (mock mode only) ----------------------------------------- */

function devPanel() {
  if (!S || !S.mock || !S.local) return "";
  return `<div class="dev" id="dev">MOCK
    <button type="button" data-dev="oven" class="${S.mock.oven ? "on" : ""}">Oven ${S.mock.oven ? "on" : "off"}</button>
    <button type="button" data-dev="fail" class="${S.mock.fail ? "on" : ""}">Next job fails</button></div>`;
}

function bindDev() {
  document.querySelectorAll("[data-dev]").forEach((b) => (b.onclick = () => {
    const k = b.dataset.dev;
    api("/api/mock/oven", k === "oven" ? { present: !S.mock.oven } : { fail: !S.mock.fail }).catch((e) => toast(e.message));
  }));
}

/* ---- Events ------------------------------------------------------------- */

function bindView(root) {
  root.querySelectorAll("[data-act]").forEach((b) => (b.onclick = () => startFlow(b.dataset.act)));
  root.querySelectorAll("input").forEach((inp) => {
    inp.addEventListener(inp.type === "radio" || inp.type === "checkbox" ? "change" : "input", () => onInput(inp));
  });
  const log = $("#j-log");
  if (log) log.addEventListener("toggle", () => {
    ui.logOpen = log.open;
    const box = $("#j-logbox");
    if (box && log.open) box.scrollTop = box.scrollHeight;
  });
  bindDev();
}

function startFlow(action) {
  setView("options", { action, form: {}, dests: null, images: null, destErr: "" });
  if (action === "backup" || action === "restore") loadDests();
}

async function loadDests() {
  ui.dests = null;
  ui.destErr = "";
  if (ui.view === "options") render();
  try {
    const r = await api("/api/destinations");
    ui.dests = r.items;
    if (!r.items.some((d) => d.tag === ui.form.dest)) ui.form.dest = (r.items.find((d) => d.origin !== "internal") || r.items[0] || {}).tag;
  } catch (e) {
    ui.dests = [];
    ui.destErr = e.message;
  }
  if (ui.view === "options") render();
}

async function findImages() {
  ui.images = "loading";
  ui.form.image = "";
  render();
  try {
    const r = await api(`/api/images?dest=${encodeURIComponent(ui.form.dest)}&folder=${encodeURIComponent(ui.form.folder || "")}`);
    ui.images = r.items;
    const good = r.items.filter((i) => i.ok);
    if (good.length === 1) ui.form.image = good[0].name;
  } catch (e) {
    ui.images = null;
    ui.error = e.message;
  }
  render();
}

function onInput(inp) {
  const f = ui.form;
  if (inp.type === "checkbox") f[inp.name] = inp.checked;
  else if (inp.type === "radio") f[inp.name] = inp.value;
  else f[inp.name] = inp.value;
  if (inp.type === "checkbox" || inp.type === "radio") {
    if (inp.name === "dest" && ui.action === "restore") { ui.images = null; f.image = ""; }
    render();
    return;
  }
  if (inp.name === "folder" && ui.action === "restore" && ui.images) {
    ui.images = null; f.image = ""; render();
    const el = $("#f-folder");
    if (el) { el.focus(); el.setSelectionRange(el.value.length, el.value.length); }
    return;
  }
  // Only re-evaluate the Next button: don't rebuild the inputs being typed in.
  const next = $("#next");
  if (next) {
    let ok = true;
    if (ui.action === "custom") ok = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/.test(f.ip || "");
    if (ui.action === "restore") ok = !!(f.dest && f.image && f.confirm === "RESTORE");
    if (ui.action === "backup") ok = !!f.dest;
    next.disabled = !ok;
  }
}

function jobBody() {
  const f = ui.form;
  const body = { action: ui.action, persist: !!f.persist };
  if (ui.action === "custom") Object.assign(body, { ip: f.ip, host: f.host == null ? "oven.local" : f.host });
  if (ui.action === "backup") Object.assign(body, { dest: f.dest, folder: f.folder || "", filename: f.filename || defaultFilename() });
  if (ui.action === "restore") Object.assign(body, { dest: f.dest, folder: f.folder || "", image: f.image, confirm: f.confirm });
  return body;
}

async function go() {
  ui.busy = true;
  ui.error = "";
  try {
    const job = await api("/api/job", jobBody());
    S.job = job;
    ui.logOpen = false;
    render();
  } catch (e) {
    ui.error = e.message;
    render();
  } finally {
    ui.busy = false;
    updateDetect();
  }
}

async function onDo(k, el) {
  switch (k) {
    case "home": return setView("home");
    case "back": return setView("options");
    case "next": return setView("connect");
    case "rescan": return loadDests();
    case "find": return findImages();
    case "go": case "go-anyway": return go();
    case "cancel":
      try { await api("/api/job/cancel", {}); } catch (e) { toast(e.message); }
      return;
    case "dismiss":
      try { await api("/api/job/dismiss", {}); S.job = null; setView("home"); } catch (e) { toast(e.message); }
      return;
    case "retry": {
      // Rebuild the wizard from the failed job, so a phone that joined
      // mid-job can retry too.  A restore always asks for RESTORE again.
      const j = S.job;
      try {
        await api("/api/job/dismiss", {});
        S.job = null;
        const restore = j.action === "restore";
        setView(restore ? "options" : "connect", {
          action: j.action,
          form: Object.assign({}, j.params, restore ? { confirm: "" } : {}),
        });
        if (restore) loadDests().then(findImages);
      } catch (e) { toast(e.message); }
      return;
    }
    case "close-sheet": sheetOpen = ""; $("#sheet").close(); return;
    case "toggle-remote":
      try { await api("/api/remote", { enabled: !S.net.remote }); } catch (e) { toast(e.message); }
      return;
    case "text-mode":
      try { await api("/api/ui/text", {}); toast("Switching to the text installer…"); } catch (e) { toast(e.message); }
      return;
    case "reboot": case "poweroff":
      if (ui.armed !== k) {
        ui.armed = k;
        renderMoreSheet();
        setTimeout(() => { if (ui.armed === k) { ui.armed = ""; if (sheetOpen === "more") renderMoreSheet(); } }, 4000);
        return;
      }
      ui.armed = "";
      try { await api("/api/power", { action: k }); toast(k === "reboot" ? "Restarting…" : "Shutting down…"); } catch (e) { toast(e.message); }
      return;
  }
}

document.addEventListener("click", (e) => {
  const d = e.target.closest("[data-do]");
  if (d && !d.disabled) { e.preventDefault(); onDo(d.dataset.do, d); return; }
  const s = e.target.closest("[data-sheet]");
  if (s) { e.preventDefault(); openSheet(s.dataset.sheet); }
});
$("#phone-btn").addEventListener("click", () => openSheet("phone"));
$("#sheet").addEventListener("close", () => { sheetOpen = ""; ui.armed = ""; });
$("#sheet").addEventListener("click", (e) => { if (e.target === $("#sheet")) $("#sheet").close(); });

/* ---- Live connection ---------------------------------------------------- */

function connect() {
  const es = new EventSource("/api/events");
  let first = true;
  es.onmessage = (ev) => {
    $("#conn").hidden = true;
    const prevJob = S && S.job;
    S = JSON.parse(ev.data);
    // A finished job dismissed here or on another device: back to the start.
    if (prevJob && !S.job) ui.view = "home";
    if (first || (!prevJob) !== (!S.job)) { first = false; render(); }
    else refresh();
  };
  es.onerror = () => {
    $("#conn").hidden = false;
    // A phone whose access was switched off gets 403s: say so instead of retrying forever.
    fetch("/api/state").then(async (r) => {
      if (r.status === 403) {
        es.close();
        $("#conn").hidden = true;
        const why = ((await r.json().catch(() => ({}))).error || "Phone access is off.").replace(/\.$/, "");
        $("#main").innerHTML = `<div class="view"><p class="eyebrow">${MARK.wait}Disconnected</p><h1>${esc(why)}</h1><p class="muted">Turn on <b>Connect a phone</b> on the installer screen and scan the QR code shown there.</p></div>`;
        $("#oven-chip").innerHTML = "";
      }
    }).catch(() => {});
  };
}

// Opened from the QR code: the server has stored the token in a cookie, so
// drop it from the address bar (and from anything the phone shares).
if (/[?&]k=/.test(location.search)) history.replaceState(null, "", location.pathname);
connect();
