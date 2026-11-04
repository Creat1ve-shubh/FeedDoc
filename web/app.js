// FeedDoctor web checker. The Go rules engine runs as WebAssembly in this
// page (see cmd/feeddoctor-wasm); this file reads the file, calls it, and
// renders the report. Nothing is uploaded except a store address, which goes
// to the proxy in web/proxy.
"use strict";

const $ = (id) => document.getElementById(id);
const proxy = (document.querySelector('meta[name="feeddoctor-proxy"]') || {}).content || "";

const state = {
  source: null, // {kind: "csv"|"store", text?, input?, name, example}
  result: null,
  fix: null,
  filter: "all",
  sizes: {}, // measured main images: url -> [width, height]
  failed: new Set(), // images that would not load
};

// ---- boot -----------------------------------------------------------------

async function boot() {
  try {
    const go = new Go();
    const res = await fetch("feeddoctor.wasm");
    if (!res.ok) throw new Error(`could not load the checker (${res.status})`);
    let inst;
    if (WebAssembly.instantiateStreaming && (res.headers.get("content-type") || "").includes("wasm")) {
      inst = await WebAssembly.instantiateStreaming(res, go.importObject);
    } else {
      inst = await WebAssembly.instantiate(await res.arrayBuffer(), go.importObject);
    }
    go.run(inst.instance);
  } catch (err) {
    setStatus(`The checker failed to start: ${err.message}. Try reloading the page.`, true);
    return;
  }
  setStatus("");
  wire();
  loadExample();
}

function wire() {
  for (const tab of document.querySelectorAll(".tab")) {
    tab.addEventListener("click", () => selectTab(tab.id));
  }
  if (!proxy) {
    $("store-url").disabled = true;
    document.querySelector("#store-form button").disabled = true;
    $("store-help").textContent = "Store scans are not switched on for this copy of FeedDoctor. Upload the product CSV instead.";
  }

  const drop = $("drop");
  $("csv-file").addEventListener("change", (e) => e.target.files[0] && readFile(e.target.files[0]));
  drop.addEventListener("dragover", (e) => { e.preventDefault(); drop.classList.add("over"); });
  drop.addEventListener("dragleave", () => drop.classList.remove("over"));
  drop.addEventListener("drop", (e) => {
    e.preventDefault();
    drop.classList.remove("over");
    const f = e.dataTransfer.files[0];
    if (f) readFile(f);
  });

  let nameTimer;
  $("store-name").addEventListener("input", () => {
    clearTimeout(nameTimer);
    nameTimer = setTimeout(() => state.source && rescan(), 350);
  });

  $("store-form").addEventListener("submit", (e) => {
    e.preventDefault();
    scanStore($("store-url").value);
  });

  for (const chip of document.querySelectorAll(".chip")) {
    chip.addEventListener("click", () => {
      state.filter = chip.dataset.filter;
      for (const c of document.querySelectorAll(".chip")) c.setAttribute("aria-pressed", String(c === chip));
      renderIssues();
    });
  }

  $("download").addEventListener("click", download);
  $("show-csv").addEventListener("click", () => {
    const view = $("csv-view");
    view.hidden = !view.hidden;
    $("show-csv").setAttribute("aria-expanded", String(!view.hidden));
    $("show-csv").textContent = view.hidden ? "Show CSV" : "Hide CSV";
  });
  $("copy-csv").addEventListener("click", async () => {
    const ta = $("csv-text");
    try {
      await navigator.clipboard.writeText(ta.value);
      $("copy-csv").textContent = "Copied";
    } catch {
      ta.focus();
      ta.select();
      $("copy-csv").textContent = "Selected, press Ctrl+C";
    }
    setTimeout(() => ($("copy-csv").textContent = "Copy"), 2000);
  });
}

function selectTab(id) {
  for (const tab of document.querySelectorAll(".tab")) {
    const on = tab.id === id;
    tab.setAttribute("aria-selected", String(on));
    $(tab.getAttribute("aria-controls")).hidden = !on;
  }
}

function setStatus(msg, error = false) {
  const el = $("status");
  el.textContent = msg;
  el.classList.toggle("error", error);
}

// ---- inputs ---------------------------------------------------------------

async function loadExample() {
  try {
    const res = await fetch("sample.csv");
    if (!res.ok) return;
    state.source = { kind: "csv", text: await res.text(), name: "Example store", example: true };
    $("store-name").value = "FeedDoc Test Store";
    state.sizes = {};
    state.failed = new Set();
    rescan();
  } catch {
    /* the page still works without the example */
  }
}

function readFile(file) {
  if (file.size > 50 * 1024 * 1024) {
    setStatus("That file is over 50 MB. Export only active products, or split the export.", true);
    return;
  }
  const reader = new FileReader();
  reader.onload = () => {
    if (state.source && state.source.example) $("store-name").value = "";
    state.source = { kind: "csv", text: String(reader.result), name: file.name, example: false };
    state.sizes = {};
    state.failed = new Set();
    rescan();
  };
  reader.onerror = () => setStatus("Could not read that file. Try exporting it from Shopify again.", true);
  reader.readAsText(file);
}

async function scanStore(address) {
  if (!proxy) return;
  const store = address.trim();
  if (!store) {
    setStatus("Enter your store address first.", true);
    return;
  }
  setStatus(`Reading ${store}…`);
  try {
    const res = await fetch(`${proxy.replace(/\/$/, "")}/scan?store=${encodeURIComponent(store)}`);
    const body = await res.json();
    if (!res.ok || body.error) throw new Error(body.error || `the store answered ${res.status}`);
    state.source = { kind: "store", input: JSON.stringify(body), name: body.domain, example: false, truncated: body.truncated };
    state.sizes = {};
    state.failed = new Set();
    rescan();
  } catch (err) {
    setStatus(`Could not scan ${store}: ${err.message}`, true);
  }
}

// ---- scan -----------------------------------------------------------------

function options() {
  return JSON.stringify({ storeName: $("store-name").value.trim(), imageSizes: state.sizes });
}

function call(fn, input) {
  const out = JSON.parse(window.feeddoctor[fn](input, options()));
  if (out.error) throw new Error(out.error);
  return out;
}

function rescan() {
  const src = state.source;
  try {
    if (src.kind === "csv") {
      state.result = call("scanCSV", src.text);
      state.fix = state.result.fixes > 0 ? call("fixCSV", src.text) : null;
    } else {
      state.result = call("scanStore", src.input);
      state.fix = null;
    }
  } catch (err) {
    setStatus(err.message, true);
    return;
  }
  const pending = state.result.images.filter((u) => !state.failed.has(u));
  if (state.result.images.length && !pending.length) {
    const n = state.result.images.length;
    state.result.unchecked = state.result.unchecked.map((u) =>
      u.rule === "image.too_small" ? { ...u, reason: `${n} main image${n === 1 ? "" : "s"} could not be loaded to measure.` } : u
    );
  }
  setStatus(src.example ? "" : `Checked ${state.result.report.productsLive} live products.`);
  render();
  if (pending.length) measure(pending);
}

// measure loads main images to learn their pixel size (the CSV has none),
// then scans again so image.too_small can run. Images that fail to load are
// remembered and left unchecked.
let measuring = false;
async function measure(urls) {
  const list = urls.filter((u) => !state.failed.has(u)).slice(0, 400);
  if (measuring || !list.length) return;
  measuring = true;
  const token = state.source;
  let done = 0;
  const one = (url) =>
    new Promise((resolve) => {
      const img = new Image();
      const timer = setTimeout(() => finish(0, 0), 8000);
      function finish(w, h) {
        clearTimeout(timer);
        img.onload = img.onerror = null;
        if (w > 0 && h > 0) state.sizes[url] = [w, h];
        else state.failed.add(url);
        done++;
        if (done % 20 === 0) setStatus(`Measuring images: ${done} of ${list.length}`);
        resolve();
      }
      img.onload = () => finish(img.naturalWidth, img.naturalHeight);
      img.onerror = () => finish(0, 0);
      img.src = url;
    });
  const queue = list.slice();
  await Promise.all(Array.from({ length: 8 }, async () => {
    while (queue.length) await one(queue.shift());
  }));
  for (const u of urls.slice(400)) state.failed.add(u); // past the cap: leave unchecked
  measuring = false;
  if (state.source === token) rescan();
}

// ---- render ---------------------------------------------------------------

const SEV_ORDER = { disapproval: 0, warning: 1, opportunity: 2 };

function render() {
  const { report, unchecked } = state.result;
  $("results").hidden = false;

  const src = state.source;
  $("source-line").innerHTML = src.example
    ? `<span class="example">Example</span>This is a sample store with planted problems. Upload your own CSV above to check yours.`
    : `Results for <strong>${esc(src.name)}</strong>` + (src.truncated ? ". Only the first 2,500 products were read; upload the CSV to check the rest." : "");

  const score = $("score");
  score.textContent = `${report.healthScore}%`;
  score.className = "score-num " + (report.healthScore >= 95 ? "good" : report.healthScore >= 75 ? "ok" : "bad");
  $("score-label").textContent = `of ${report.productsLive} live products would be approved`;
  for (const k of ["disapproval", "warning", "opportunity"]) $(`c-${k}`).textContent = report.counts[k] || 0;

  renderIssues();
  renderFix();

  const ul = $("unchecked");
  ul.innerHTML = unchecked.length
    ? unchecked.map((u) => `<li><code>${esc(u.rule)}</code> ${esc(u.summary || "")}<span>${esc(u.reason)}</span></li>`).join("")
    : "<li>Every rule ran.</li>";
}

function renderIssues() {
  const { report, fixRules } = state.result;
  const shown = report.issues.filter((i) => state.filter === "all" || i.severity === state.filter);
  const box = $("issues");
  if (!shown.length) {
    box.innerHTML = `<div class="empty">${report.issues.length ? "Nothing at this level." : "No problems found in the rules that ran."}</div>`;
    return;
  }
  const groups = new Map();
  for (const is of shown) {
    const key = is.productId || "";
    if (!groups.has(key)) groups.set(key, { title: is.product || "Store", handle: is.productId, issues: [] });
    groups.get(key).issues.push(is);
  }
  const ordered = [...groups.values()].sort((a, b) => {
    if (!a.handle) return -1;
    if (!b.handle) return 1;
    const wa = Math.min(...a.issues.map((i) => SEV_ORDER[i.severity]));
    const wb = Math.min(...b.issues.map((i) => SEV_ORDER[i.severity]));
    return wa - wb || b.issues.length - a.issues.length;
  });
  box.innerHTML = ordered
    .map((g) => {
      g.issues.sort((a, b) => SEV_ORDER[a.severity] - SEV_ORDER[b.severity]);
      const head = g.handle
        ? `<span class="product-title">${esc(g.title)}</span><span class="product-handle">${esc(g.handle)}</span>`
        : `<span class="product-title">Store</span><span class="product-handle">account level</span>`;
      const rows = g.issues
        .map((i) => {
          const fixed = fixRules[`${i.rule}|${i.productId}`] && state.fix ? `<span class="fixed-tag">Fixed in download</span>` : "";
          return `<div class="issue">
            <span class="pill ${i.severity}">${i.severity}</span>
            <div class="issue-body">
              <p class="issue-msg">${esc(i.message)}</p>
              <p class="issue-fix">${esc(i.fix)}</p>
              <div class="issue-meta"><span>${esc(i.rule)}</span><span>· ${esc(i.field)}</span>${fixed}</div>
            </div>
          </div>`;
        })
        .join("");
      return `<article class="product"><div class="product-head">${head}</div>${rows}</article>`;
    })
    .join("");
}

function renderFix() {
  const fix = state.fix;
  const box = $("fix-box");
  if (state.source.kind !== "csv") {
    box.hidden = false;
    $("fix-summary").textContent = "Upload the product CSV to get a fixed file. Store scans only report.";
    for (const id of ["download", "show-csv"]) $(id).disabled = true;
    $("csv-view").hidden = true;
    document.querySelector(".changes").hidden = true;
    document.querySelector(".howto").hidden = true;
    return;
  }
  document.querySelector(".changes").hidden = false;
  document.querySelector(".howto").hidden = false;
  if (!fix || !fix.changes.length) {
    $("fix-summary").textContent = "Nothing here can be fixed automatically. The fixes above need your input.";
    for (const id of ["download", "show-csv"]) $(id).disabled = true;
    $("change-count").textContent = "0";
    $("changes").querySelector("tbody").innerHTML = "";
    $("csv-text").value = "";
    return;
  }
  for (const id of ["download", "show-csv"]) $(id).disabled = false;
  const products = new Set(fix.changes.map((c) => c.handle)).size;
  $("fix-summary").textContent = `${fix.changes.length} change${fix.changes.length === 1 ? "" : "s"} across ${products} product${products === 1 ? "" : "s"}: promotional titles, fake discounts, missing condition, brand and GTIN exemptions.`;
  $("change-count").textContent = fix.changes.length;
  $("changes").querySelector("tbody").innerHTML = fix.changes
    .map((c) => `<tr><td>${c.line}</td><td>${esc(c.column)}</td><td class="before">${esc(c.before) || "<em>empty</em>"}</td><td class="after">${esc(c.after) || "<em>empty</em>"}</td></tr>`)
    .join("");
  $("csv-text").value = fix.importCsv;
}

function download() {
  if (!state.fix) return;
  const blob = new Blob([state.fix.importCsv], { type: "text/csv" });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  const base = (state.source.name || "products").replace(/\.csv$/i, "");
  a.download = `${base}-fixed.csv`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

boot();
