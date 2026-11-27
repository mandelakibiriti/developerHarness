"use strict";

// Shared contract for the shell and the swarm modules (filetree.js, telemetry.js).
const state = {
  tools: [], profile: "", level: "beginner", gates: [], budget: null, scope: "",
  providers: [], llm: null, available: [],
};

// Harness UI component state
const harness = {
  agentStatus: "idle",
  budgetSent: 0,
};

// Helper function
function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

async function fetchJSON(url, opts) {
  const r = await fetch(url, opts);
  const text = await r.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch (e) { data = { error: text }; }
  if (!r.ok) throw new Error(data && data.error ? data.error : ("HTTP " + r.status));
  return data;
}

async function setLevel(level) {
  const d = await fetchJSON("/api/level", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ level }),
  });
  state.level = d.level;
  document.getElementById("level-select").value = d.level;
  return d;
}

// The top-bar model selector is the single source of truth: every tab's chat
// and generation reads state.llm.provider/model, so selections stay consistent.
async function sendChat(messages, mode, opts) {
  opts = opts || {};
  const prov = opts.provider !== undefined ? opts.provider : (state.llm && state.llm.provider);
  const model = opts.model !== undefined ? opts.model : (state.llm && state.llm.model);
  return fetchJSON("/api/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ messages, mode, level: state.level, provider: prov, model }),
  });
}

function currentModel() {
  return { provider: state.llm && state.llm.provider, model: state.llm && state.llm.model };
}

window.H = { el, fetchJSON, state, setLevel, sendChat };

function renderScopeMetric(container) {
  if (!state.scope) return;

  const card = el('harness-metric-card', '', '');
  card.className = 'harness-metric-card status-info';
  card.setAttribute('title', 'Scope');

  const title = el('div', 'mc-title', 'Scope');
  card.appendChild(title);

  const value = el('div', 'mc-value', state.scope);
  card.appendChild(value);

  const status = el('div', 'mc-status', 'configured');
  status.classList.add('status-info');
  card.appendChild(status);

  container.appendChild(card);
}

// ---- tabs ----

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "telemetry", label: "Telemetry" },
  { id: "trail", label: "Trail" },
  { id: "architect", label: "Architect" },
  { id: "map", label: "Map" },
  { id: "graph", label: "Graph" },
  { id: "explorer", label: "Explorer" },
  { id: "settings", label: "Settings" },
  { id: "evals", label: "Evals" },
];

function renderTabs() {
  const bar = document.getElementById("tabs");
  bar.innerHTML = "";
  TABS.forEach((t, i) => {
    const b = el("button", "tab" + (i === 0 ? " active" : ""), t.label);
    b.dataset.tab = t.id;
    b.onclick = () => showPane(t.id, b);
    bar.appendChild(b);
  });
}

function showPane(id, btn) {
  btn = btn || document.querySelector('.tab[data-tab="' + id + '"]');
  document.querySelectorAll(".pane").forEach((p) => (p.hidden = true));
  document.getElementById("pane-" + id).hidden = false;
  document.querySelectorAll(".tab").forEach((b) => b.classList.remove("active"));
  if (btn) btn.classList.add("active");
  if (id === "telemetry" && window.Telemetry) window.Telemetry.show(document.getElementById("pane-telemetry"));
  if (id === "architect") renderArchitect();
  if (id === "map") renderMap();
  if (id === "graph") renderGraph();
  if (id === "trail") renderTrail();
  if (id === "explorer") renderExplorer();
  if (id === "settings") renderSettings();
  if (id === "evals" && window.Evals) window.Evals.show(document.getElementById("pane-evals"));
}

// ---- status ----

async function loadStatus() {
  try {
    const s = await fetchJSON("/api/status");
    state.profile = s.profile;
    state.level = s.level;
    state.tools = s.tools || [];
    state.scope = s.scope || "";
    document.getElementById("profile").textContent = s.profile || "";
    if (s.project) { // several instances can be open at once: say which folder this one tracks
      const name = s.project.split(/[\\/]/).filter(Boolean).pop();
      const badge = document.getElementById("project");
      badge.textContent = name;
      badge.title = s.project;
      badge.hidden = false;
      document.title = name + " · DevEx Harness";
    }
    document.getElementById("level-select").value = s.level || "beginner";
    renderOverview();
  } catch (e) { /* not ready */ }
}

// ---- global model selector (top bar) ----

// ---- top-bar model picker: every model whose provider key works, grouped by provider ----

async function loadAvailable(refresh) {
  try {
    const d = await fetchJSON("/api/llm/available" + (refresh ? "?refresh=1" : ""));
    state.available = d.providers || [];
  } catch (e) { state.available = []; }
  renderModelSelector();
  return state.available;
}

function renderModelSelector() {
  const sel = document.getElementById("model-select");
  if (!sel) return;
  sel.innerHTML = "";
  const working = state.available.filter((p) => p.ok && p.models.length);
  sel.disabled = !working.length;
  if (!working.length) {
    sel.appendChild(el("option", "", "No working key. Add one in Settings"));
    return;
  }
  for (const p of working) {
    const group = el("optgroup", "");
    group.label = p.label || p.provider;
    for (const m of p.models) {
      const o = el("option", "", m);
      o.value = JSON.stringify([p.provider, m]);
      group.appendChild(o);
    }
    sel.appendChild(group);
  }
  const cur = state.llm && JSON.stringify([state.llm.provider, state.llm.model]);
  if (cur && [...sel.options].some((o) => o.value === cur)) sel.value = cur;
  else sel.dispatchEvent(new Event("change")); // saved model no longer works: use the first that does
}

async function ensureProviders() {
  if (state.providers.length) return;
  try {
    const d = await fetchJSON("/api/llm");
    state.providers = d.providers || [];
    state.llm = d;
  } catch (e) { /* no llm */ }
}

// ---- overview ----

function renderOverview() {
  const ov = document.getElementById("overview");
  ov.innerHTML = "";

  // Harness UI: Metric Cards container
  const metricContainer = el("div", "metric-cards");
  ov.appendChild(metricContainer);
  renderScopeMetric(metricContainer);

  // healthy tools are already listed under "Tools on your machine"; only surface failures here
  for (const t of state.tools.filter((x) => x.status === "error")) {
    const row = el("div", "tool-row");
    row.appendChild(el("span", "status-dot " + (t.healthy ? "ok" : t.status === "error" ? "err" : "warn")));
    row.appendChild(el("span", "tool-name", t.tab + " (" + t.id + ")"));
    row.appendChild(el("span", "tool-status", t.status + (t.error ? ": " + t.error : "")));
    if (t.upstream) {
      const a = el("a", "open", "open ↗");
      a.href = t.upstream; a.target = "_blank"; a.rel = "noopener";
      row.appendChild(a);
    }
    ov.appendChild(row);
  }
}

// ---- overview: external tools found on this machine ----

async function loadApps() {
  const box = document.getElementById("apps");
  const refresh = document.getElementById("apps-refresh");
  refresh.disabled = true;
  box.replaceChildren(el("p", "hint", "Looking for tools…"));
  try {
    const apps = (await fetchJSON("/api/apps")).apps || [];
    box.replaceChildren(...apps.map(appRow));
    if (!apps.length) box.replaceChildren(el("p", "hint", "No supported tools found on this machine."));
  } catch (e) {
    box.replaceChildren(el("p", "hint", "Could not scan for tools: " + e.message));
  } finally {
    refresh.disabled = false;
  }
}

function appRow(a) {
  const row = el("div", "app-row");
  const dot = el("span", "status-dot " + (a.running ? "ok" : "idle"));
  dot.title = a.running ? "running" : "installed";
  const text = el("div", "app-text");
  text.appendChild(el("span", "tool-name", a.name));
  text.appendChild(el("span", "hint", a.about + (a.running ? " · running" : "")));
  row.appendChild(dot);
  row.appendChild(text);
  if (a.url) {
    const link = el("a", "btn secondary", "Open ↗");
    link.href = a.url; link.target = "_blank"; link.rel = "noopener";
    row.appendChild(link);
  }
  if (a.launchable) {
    const b = el("button", "btn secondary", "Launch");
    b.setAttribute("aria-label", "Launch " + a.name);
    b.onclick = async () => {
      b.disabled = true;
      try {
        await fetchJSON("/api/apps", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ id: a.id }) });
        b.textContent = "Launched";
      } catch (e) { b.textContent = "Failed"; b.title = e.message; }
      setTimeout(() => { b.textContent = "Launch"; b.disabled = false; }, 2000);
    };
    row.appendChild(b);
  } else if (!a.url && a.hint) {
    const code = el("code", "app-hint", a.hint);
    code.title = "Start it from a terminal";
    row.appendChild(code);
  }
  return row;
}

function initOverview() {
  const about = document.getElementById("ov-about");
  if (localStorage.getItem("ov-about") === "closed") about.open = false;
  about.addEventListener("toggle", () => localStorage.setItem("ov-about", about.open ? "open" : "closed"));
  document.getElementById("apps-refresh").onclick = loadApps;
  loadApps();
}

// ---- chat component ----

// contextFn (optional) returns text the agent should see with the next turn, e.g.
// the editor buffer; it is sent only when it changed, to keep history small.
function buildChat(mode, contextFn) {
  const wrap = el("div", "chat");
  const log = el("div", "chat-log");
  const inputWrap = el("div", "chat-input");
  const input = el("textarea", "", "");
  input.placeholder = "Ask about the architecture, requirements, or next step…";
  const send = el("button", "btn", "Send");
  const history = [];
  inputWrap.appendChild(input);
  inputWrap.appendChild(send);
  wrap.appendChild(log);
  wrap.appendChild(inputWrap);

  function append(role, content) {
    const msg = el("div", "msg " + role);
    msg.appendChild(el("div", "role", role === "user" ? "You" : "Assistant"));
    const bubble = el("div", "bubble", content);
    msg.appendChild(bubble);
    log.appendChild(msg);
    log.scrollTop = log.scrollHeight;
    return bubble;
  }

  let lastCtx = "";
  async function ask(text) {
    if (!text || send.disabled) return;
    append("user", text);
    send.disabled = true; // before awaiting context, so a second Enter can't double-send
    const ctx = contextFn ? await contextFn(text) : "";
    const withCtx = ctx && ctx !== lastCtx ? text + "\n\n" + ctx : text;
    lastCtx = ctx || lastCtx;
    history.push({ role: "user", content: withCtx });
    const bubble = append("assistant", "…");
    try {
      const d = await sendChat(history, mode, {});
      bubble.textContent = d.reply;
      history.push({ role: "assistant", content: d.reply });
    } catch (e) {
      bubble.textContent = "Error: " + e.message;
      bubble.style.color = "var(--err)";
    } finally {
      send.disabled = false;
    }
  }

  function sendTurn() {
    if (send.disabled) return; // keep the draft while a reply is pending
    const text = input.value.trim();
    input.value = "";
    ask(text);
  }

  send.onclick = sendTurn;
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); sendTurn(); }
  });

  return { wrap, append, history, input, ask };
}

// ---- architect ----

async function renderArchitect() {
  const pane = document.getElementById("pane-architect");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  // ---- 1. generate bar ----
  const gen = el("section", "gen-card");
  const genHead = el("div", "gen-head");
  genHead.appendChild(el("span", "gen-title", "Generate architecture spec"));
  genHead.appendChild(el("span", "hint", "Writes architecture.md, functional-requirements.md and api-contract.md to /docs"));
  gen.appendChild(genHead);
  const genRow = el("div", "row");
  const goal = el("input", "", "");
  goal.placeholder = "What are you building? e.g. 'A todo app: web UI + REST API + Postgres'";
  goal.setAttribute("aria-label", "Project goal");
  const genBtn = el("button", "btn", "Generate spec");
  const stopBtn = el("button", "btn destructive", "■ Stop");
  stopBtn.hidden = true;
  genRow.appendChild(goal);
  genRow.appendChild(genBtn);
  genRow.appendChild(stopBtn);
  gen.appendChild(genRow);
  const genStatus = el("p", "test-out", "");
  genStatus.setAttribute("role", "status");
  gen.appendChild(genStatus);
  pane.appendChild(gen);

  // ---- 2. spec editor (left) | agent chat (right) ----
  const grid = el("div", "ide ide-docs");
  const docCard = el("section", "ide-card");
  const docHead = el("div", "ide-head");
  docHead.appendChild(el("span", "", "Specification"));
  const docTabs = el("div", "doc-tabs");
  docTabs.setAttribute("role", "tablist");
  docHead.appendChild(docTabs);
  docHead.appendChild(el("span", "spacer"));
  const dirtyLabel = el("span", "sub", "");
  const saveBtn = el("button", "btn", "Save");
  saveBtn.title = "Save (⌘/Ctrl + S)";
  docHead.appendChild(dirtyLabel);
  docHead.appendChild(saveBtn);
  docCard.appendChild(docHead);
  const docWrap = el("div", "editor mono");
  const docTa = el("textarea", "", "");
  docTa.spellcheck = true;
  docTa.setAttribute("aria-label", "Specification markdown");
  docWrap.appendChild(docTa);
  docCard.appendChild(docWrap);

  const chatCard = el("section", "ide-card");
  const chatHead = el("div", "ide-head");
  chatHead.appendChild(el("span", "", "Discuss with the agent"));
  const chatSub = el("span", "sub", "");
  chatHead.appendChild(chatSub);
  chatCard.appendChild(chatHead);

  let docs = [];      // file names in /docs
  let current = null; // open file name
  let savedText = ""; // last saved content, for the unsaved-changes guard

  const chat = buildChat("architect", () => {
    if (!current) return "";
    return "Specification document open in the editor: docs/" + current +
      " (other docs: " + (docs.filter((d) => d !== current).join(", ") || "none") + ")\n\n" +
      docTa.value.slice(0, 16000);
  });
  chat.input.placeholder = "Discuss the spec: gaps, risks, trade-offs… (Enter to send)";
  chatCard.appendChild(chat.wrap);
  chat.append("assistant", "Generate a spec above or open one on the left. I can see the document you have open, so ask me to review it, find gaps, or weigh trade-offs.");

  grid.appendChild(docCard);
  grid.appendChild(chatCard);
  pane.appendChild(grid);

  // ---- footer ----
  const reviewBtn = el("button", "btn secondary", "Review with Plannotator");
  const scaleLink = el("a", "hint", "Architecture principles → designed-to-scale.vercel.app");
  scaleLink.href = "https://designed-to-scale.vercel.app/";
  scaleLink.target = "_blank";
  scaleLink.rel = "noopener";
  const bottom = el("div", "chat-actions");
  bottom.appendChild(reviewBtn);
  bottom.appendChild(scaleLink);
  pane.appendChild(bottom);

  // ---- behaviour ----
  const isDirty = () => current !== null && docTa.value !== savedText;
  const confirmDiscard = () => !isDirty() || confirm("Discard unsaved changes to " + current + "?");
  function setStatus(text, cls) { genStatus.textContent = text; genStatus.className = "test-out " + (cls || ""); }
  function updateDirty() { dirtyLabel.textContent = isDirty() ? "Unsaved changes" : ""; saveBtn.disabled = !isDirty(); }

  function renderDocTabs() {
    docTabs.innerHTML = "";
    for (const name of docs) {
      const b = el("button", "doc-tab" + (name === current ? " active" : ""), name.replace(/\.md$/, ""));
      b.setAttribute("role", "tab");
      b.setAttribute("aria-selected", String(name === current));
      b.title = "docs/" + name;
      b.onclick = () => { if (name !== current && confirmDiscard()) openDoc(name); };
      docTabs.appendChild(b);
    }
  }

  function showEmpty() {
    current = null;
    savedText = "";
    docTa.value = "";
    docTa.disabled = true;
    docTa.placeholder = "No specification yet. Describe what you are building above and click Generate spec.";
    chatSub.textContent = "";
    renderDocTabs();
    updateDirty();
  }

  async function openDoc(name) {
    try {
      const d = await fetchJSON("/api/docs?name=" + encodeURIComponent(name));
      current = name;
      savedText = d.content;
      docTa.disabled = false;
      docTa.value = d.content;
      docTa.scrollTop = 0;
      chatSub.textContent = "· sees docs/" + name;
      renderDocTabs();
      updateDirty();
    } catch (e) { setStatus("✗ Could not open " + name + ": " + e.message, "err"); }
  }

  async function loadDocs(openName) {
    try {
      const d = await fetchJSON("/api/docs");
      docs = (d.files || []).map((f) => f.name);
    } catch (e) { docs = []; }
    const target = openName && docs.includes(openName) ? openName : (docs.includes(current) ? current : docs[0]);
    if (target) await openDoc(target); else showEmpty();
  }

  async function save() {
    if (!isDirty()) return;
    saveBtn.disabled = true;
    try {
      await fetchJSON("/api/docs?name=" + encodeURIComponent(current), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ content: docTa.value }),
      });
      savedText = docTa.value;
      dirtyLabel.textContent = "Saved ✓";
      setTimeout(updateDirty, 1200);
    } catch (e) {
      dirtyLabel.textContent = "Save failed: " + e.message;
      saveBtn.disabled = false;
    }
  }

  docTa.addEventListener("input", updateDirty);
  docTa.addEventListener("keydown", (e) => {
    if (e.key === "s" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); save(); }
  });
  saveBtn.onclick = save;
  goal.addEventListener("keydown", (e) => { if (e.key === "Enter" && !genBtn.hidden) genBtn.click(); });
  window.addEventListener("beforeunload", (e) => { if (isDirty()) e.preventDefault(); });

  let ctrl = null;
  function setGenerating(on) {
    genBtn.hidden = on;
    stopBtn.hidden = !on;
    goal.disabled = on;
  }

  genBtn.onclick = async () => {
    const g = goal.value.trim();
    if (!g) { setStatus("Describe what you are building first.", "err"); goal.focus(); return; }
    if (!confirmDiscard()) return;
    const m = currentModel();
    ctrl = new AbortController();
    setGenerating(true);
    const t0 = Date.now();
    const label = "Generating with " + (m.model || "the default model") + "… ";
    setStatus(label + "0s");
    const timer = setInterval(() => setStatus(label + Math.round((Date.now() - t0) / 1000) + "s"), 1000);
    try {
      const d = await fetchJSON("/api/docs/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ goal: g, level: state.level, provider: m.provider, model: m.model }),
        signal: ctrl.signal,
      });
      setStatus("✓ Wrote " + d.files.join(", ") + " in " + Math.round((Date.now() - t0) / 1000) + "s", "ok");
      await loadDocs(d.files[0]);
    } catch (e) {
      if (e.name === "AbortError") setStatus("Stopped. Nothing was written.");
      else setStatus("✗ " + e.message, "err");
    } finally {
      clearInterval(timer);
      setGenerating(false);
      ctrl = null;
    }
  };
  stopBtn.onclick = () => { if (ctrl) ctrl.abort(); };

  // Plannotator opens in the browser; poll until the reviewer approves or sends feedback.
  async function waitForReview() {
    reviewBtn.disabled = true;
    reviewBtn.textContent = "Reviewing in Plannotator…";
    let st;
    try {
      do {
        await new Promise((r) => setTimeout(r, 3000));
        st = await fetchJSON("/api/review");
      } while (st.status === "running");
      if (st.status === "done") {
        const res = st.result || {};
        const notes = Array.isArray(res.annotations) ? res.annotations.length + " annotation(s). " : "";
        const feedback = typeof res.feedback === "string" ? res.feedback : "";
        chat.append("assistant", "Plannotator review: " + st.decision + ". " + notes + feedback);
      } else {
        chat.append("assistant", "Plannotator review failed: " + (st.error || st.status));
      }
    } catch (e) {
      chat.append("assistant", "Lost track of the review: " + e.message);
    } finally {
      reviewBtn.disabled = false;
      reviewBtn.textContent = "Review with Plannotator";
    }
  }

  reviewBtn.onclick = async () => {
    try {
      await fetchJSON("/api/review", { method: "POST" });
      chat.append("assistant", "Opened the spec in Plannotator. Approve it or send feedback there and I'll show the result here.");
      waitForReview();
    } catch (e) { chat.append("assistant", "Error: " + e.message); }
  };

  loadDocs();
}

// ---- map (floorplan visual + chat) ----

async function renderMap() {
  const pane = document.getElementById("pane-map");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  const gen = el("section", "gen-card");
  const genHead = el("div", "gen-head");
  genHead.appendChild(el("span", "gen-title", "Generate blueprint"));
  genHead.appendChild(el("span", "hint", "Derived from the /docs spec: modules, endpoints, screens and build order → floorplan/plan.json"));
  gen.appendChild(genHead);
  const row = el("div", "row");
  const goal = el("input", "", "");
  goal.placeholder = "Goal for the blueprint, e.g. 'Modular monolith for the URL shortener spec'";
  goal.setAttribute("aria-label", "Blueprint goal");
  const genBtn = el("button", "btn", "Generate blueprint");
  row.appendChild(goal);
  row.appendChild(genBtn);
  gen.appendChild(row);
  const status = el("p", "test-out", "");
  status.setAttribute("role", "status");
  gen.appendChild(status);
  pane.appendChild(gen);

  const viz = el("div", "fp");
  pane.appendChild(viz);

  // the chat sees the blueprint and whichever module is selected on the map
  let plan = null, selected = null;
  const chat = buildChat("architect", () => plan
    ? "Floorplan blueprint (floorplan/plan.json):\n```json\n" + JSON.stringify(plan) + "\n```" +
      (selected ? "\nSelected on the map: " + selected.name + " (" + selected.id + ")" : "")
    : "");
  chat.input.placeholder = "Discuss the blueprint: boundaries, build order, risks… (Enter to send)";
  pane.appendChild(el("p", "pane-title", "Discuss the blueprint"));
  pane.appendChild(chat.wrap);

  async function load() {
    try {
      const d = await fetchJSON("/api/floorplan");
      if (!d.exists) { plan = null; viz.replaceChildren(el("p", "hint", d.hint || "No blueprint yet. Generate one above.")); return; }
      plan = d.plan;
      window.Floorplan.render(viz, plan, { onSelect: (n) => { selected = n; } });
    } catch (e) { viz.replaceChildren(el("p", "hint", "Could not load the blueprint: " + e.message)); }
  }

  genBtn.onclick = async () => {
    genBtn.disabled = true;
    const t0 = Date.now();
    const timer = setInterval(() => (status.textContent = "Generating… " + Math.round((Date.now() - t0) / 1000) + "s"), 1000);
    status.className = "test-out";
    status.textContent = "Generating…";
    try {
      const m = currentModel();
      await fetchJSON("/api/floorplan/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ goal: goal.value.trim() || "the project", level: state.level, provider: m.provider, model: m.model }),
      });
      status.className = "test-out ok";
      status.textContent = "✓ Blueprint written to floorplan/plan.json";
      await load();
    } catch (e) {
      status.className = "test-out err";
      status.textContent = "✗ " + e.message;
    } finally {
      clearInterval(timer);
      genBtn.disabled = false;
    }
  };

  load();
}

// ---- graph (visual + traverse + chat) ----

async function renderGraph() {
  const pane = document.getElementById("pane-graph");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  // ---- left: search + graphify's graph.html (sandboxed) ----
  const grid = el("div", "ide ide-graph");
  const left = el("section", "ide-card");
  const head = el("div", "ide-head graph-head");
  const search = el("input", "", "");
  search.type = "search";
  search.placeholder = "Search nodes by name or file…";
  search.setAttribute("aria-label", "Search graph nodes");
  const stats = el("span", "sub", "Loading…");
  const sideBtn = el("button", "btn secondary", "Communities");
  sideBtn.setAttribute("aria-pressed", "false");
  head.appendChild(search);
  head.appendChild(stats);
  head.appendChild(sideBtn);
  left.appendChild(head);
  const results = el("div", "graph-results");
  results.hidden = true;
  left.appendChild(results);
  const frame = el("iframe", "graph-frame");
  frame.title = "Codebase knowledge graph";
  frame.setAttribute("sandbox", "allow-scripts"); // never allow-same-origin: see graphview.go
  left.appendChild(frame);

  // ---- right: selected node + chat ----
  const right = el("section", "ide-card");
  const rhead = el("div", "ide-head");
  rhead.appendChild(el("span", "", "Discuss the graph"));
  const sub = el("span", "sub", "");
  rhead.appendChild(sub);
  right.appendChild(rhead);
  const node = el("div", "graph-node");
  node.hidden = true;
  right.appendChild(node);

  let byId = new Map(), links = new Map(), nodes = [], selected = null;
  const linksOf = (id) => links.get(id) || [];
  const name = (id) => (byId.get(id) || {}).label || id;
  const describe = (l, id) => (l.source === id ? "→ " + l.relation + " " + name(l.target) : "← " + name(l.source) + " " + l.relation + " this");

  // each question is grounded with the selected node's edges and a `graphify query` traversal
  const chat = buildChat("general", async (question) => {
    let ctx = "Codebase knowledge graph (graphify): " + nodes.length + " nodes.";
    const n = selected && byId.get(selected);
    if (n) {
      ctx += "\nSelected node: " + n.label + " (" + (n.source_file || "no file") + ")\nConnections:\n" +
        linksOf(n.id).slice(0, 60).map((l) => describe(l, n.id)).join("\n");
    }
    try {
      const d = await fetchJSON("/api/graph/query", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ question: n ? question + " " + n.label : question }) });
      ctx += "\nGraph traversal for this question:\n" + d.answer.slice(0, 6000);
    } catch (e) { /* graphify CLI missing: the node context above still applies */ }
    return ctx;
  });
  chat.input.placeholder = "Ask about the codebase and its connections… (Enter to send)";
  chat.append("assistant", "Search or click a node, then ask how it connects to the rest of the codebase.");
  right.appendChild(chat.wrap);
  grid.appendChild(left);
  grid.appendChild(right);
  pane.appendChild(grid);

  const send = (msg) => frame.contentWindow && frame.contentWindow.postMessage(msg, "*"); // opaque origin
  const sendTheme = () => send({ type: "theme", theme: document.documentElement.dataset.theme });
  new MutationObserver(sendTheme).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });

  function select(id) {
    const n = byId.get(id);
    if (!n) return;
    selected = id;
    sub.textContent = "· about " + n.label;
    node.hidden = false;
    node.replaceChildren(el("strong", "", n.label), el("div", "hint", n.source_file || "no source file"));
    const ls = linksOf(id);
    node.appendChild(el("div", "hint", ls.length + " connections"));
    for (const l of ls) {
      const other = l.source === id ? l.target : l.source;
      const b = el("button", "graph-link", describe(l, id));
      b.onclick = () => { select(other); send({ type: "focus", id: other }); };
      node.appendChild(b);
    }
    node.scrollTop = 0;
  }

  window.addEventListener("message", (e) => {
    if (e.source !== frame.contentWindow || !e.data || e.data.graph !== 1) return;
    if (e.data.type === "ready") sendTheme();
    if (e.data.type === "select") select(e.data.id);
    if (e.data.type === "missing") stats.textContent = "That node isn't drawn in this view";
  });

  const matches = () => {
    const q = search.value.trim().toLowerCase();
    if (!q) return [];
    return nodes.filter((n) => (n.label + " " + (n.source_file || "")).toLowerCase().includes(q))
      .sort((a, b) => b.label.toLowerCase().startsWith(q) - a.label.toLowerCase().startsWith(q)).slice(0, 30);
  };
  function pick(id) {
    results.hidden = true;
    select(id);
    send({ type: "focus", id });
  }
  search.addEventListener("input", () => {
    const found = matches();
    results.hidden = !search.value.trim();
    results.replaceChildren(...found.map((n) => {
      const b = el("button", "graph-link", n.label);
      b.appendChild(el("span", "hint", " " + (n.source_file || "")));
      b.onclick = () => pick(n.id);
      return b;
    }));
    if (!found.length) results.replaceChildren(el("p", "hint", "No matching nodes."));
  });
  search.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { const first = matches()[0]; if (first) pick(first.id); }
    if (e.key === "Escape") results.hidden = true;
  });
  sideBtn.onclick = () => {
    const open = sideBtn.getAttribute("aria-pressed") !== "true";
    sideBtn.setAttribute("aria-pressed", String(open));
    send({ type: "sidebar", open });
  };

  try {
    const d = await fetchJSON("/api/graph");
    if (!d.exists) {
      stats.textContent = "No graph yet";
      frame.replaceWith(el("p", "hint graph-empty", d.hint));
      return;
    }
    nodes = d.graph.nodes || [];
    byId = new Map(nodes.map((n) => [n.id, n]));
    const all = d.graph.links || d.graph.edges || [];
    for (const l of all) for (const id of new Set([l.source, l.target])) {
      if (!links.has(id)) links.set(id, []);
      links.get(id).push(l);
    }
    stats.textContent = nodes.length + " nodes · " + all.length + " links";
    frame.src = "/graph/view";
  } catch (e) { stats.textContent = "Graph unavailable: " + e.message; }
}

// ---- build ----

// ---- activity: the project timeline, shown in Trail > Live activity ----

const STALE_MS = 15 * 60 * 1000; // longest a model call can last (llm client timeout)
const activity = { events: [], running: new Map(), render: null };

function trackActivity(ev) {
  const d = ev.data || {};
  const key = d.purpose + "|" + d.provider + "|" + d.model;
  if (ev.ev === "harness-start") activity.running.clear(); // calls from a previous process are gone
  if (ev.ev === "llm-request") activity.running.set(key, ev);
  if (ev.ev === "llm-usage" || ev.ev === "llm-error") activity.running.delete(key);
  activity.events.unshift(ev);
  if (activity.events.length > 200) activity.events.pop();
  updateActivity();
}

function runningCalls() {
  return [...activity.running.values()].filter((ev) => Date.now() - new Date(ev.t) < STALE_MS);
}

function updateActivity() {
  const tab = document.querySelector('.tab[data-tab="trail"]');
  const busy = runningCalls().length > 0;
  if (tab) { tab.classList.toggle("busy", busy); tab.title = busy ? "The agent is working" : ""; }
  if (activity.render) activity.render();
}

function activityLine(ev) {
  if (ev.ev === "PostToolUse") return "Coding agent used " + ev.tool;
  if (ev.ev === "stop") return "Coding agent finished its turn";
  const d = ev.data || {};
  if (ev.ev === "run") return "Ran " + d.lang + " code · exit " + d.exit;
  return eventSummary(ev) || (Object.keys(d).length ? JSON.stringify(d) : ev.ev);
}

function activityItem(ev, running) {
  const alert = ["llm-error", "tool-error", "budget-alert"].includes(ev.ev);
  const li = el("li", "event" + (alert ? " alert" : "") + (running ? " running" : ""));
  li.appendChild(el("span", "ev-time", new Date(ev.t).toLocaleTimeString()));
  li.appendChild(el("span", "ev-name", running ? "Working: " + ev.data.purpose : ev.ev));
  li.appendChild(el("span", "ev-data", running ? ev.data.model + " · " + Math.round((Date.now() - new Date(ev.t)) / 1000) + "s · " + (ev.data.preview || "") : activityLine(ev)));
  return li;
}

// ---- trail: Agent Trail full width, or the live activity timeline ----

async function renderTrail() {
  const pane = document.getElementById("pane-trail");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  const card = el("section", "ide-card trail-card");
  const head = el("div", "ide-head");
  const views = el("div", "doc-tabs");
  const trailBtn = el("button", "doc-tab", "Agent Trail");
  const liveBtn = el("button", "doc-tab", "Live activity");
  views.appendChild(trailBtn);
  views.appendChild(liveBtn);
  head.appendChild(views);
  const sub = el("span", "sub", "");
  head.appendChild(sub);
  head.appendChild(el("span", "spacer"));
  const reload = el("button", "btn secondary", "Reload");
  const open = el("a", "btn secondary", "Open in new tab ↗");
  open.target = "_blank";
  open.rel = "noopener";
  open.hidden = true;
  head.appendChild(reload);
  head.appendChild(open);
  card.appendChild(head);
  const trail = el("div", "trail");
  const list = el("ul", "events activity-list");
  list.setAttribute("aria-label", "Activity timeline");
  card.appendChild(trail);
  card.appendChild(list);
  pane.appendChild(card);

  activity.render = () => {
    const running = runningCalls();
    list.replaceChildren(
      ...running.map((ev) => activityItem(ev, true)),
      ...activity.events.filter((ev) => !running.includes(ev)).map((ev) => activityItem(ev, false)));
  };
  activity.render();
  setInterval(() => { if (runningCalls().length) updateActivity(); }, 1000); // elapsed time

  function help(text, command) {
    const box = el("div", "trail-help");
    box.appendChild(el("p", "", text));
    if (command) box.appendChild(el("code", "", command));
    trail.replaceChildren(box);
  }

  // The harness starts Agent Trail itself, so wait a while for it to come up.
  let attempt = 0;
  let tries = 0;
  async function loadTrail(run) {
    let app;
    try {
      app = ((await fetchJSON("/api/apps")).apps || []).find((a) => a.id === "trail");
      await loadStatus();
      // harness.json decides the port when the harness runs Agent Trail itself
      const own = state.tools.find((t) => t.id === "trail" && t.healthy && t.upstream);
      if (own) app = { running: true, url: own.upstream };
    }
    catch (e) { help("Could not check for Agent Trail: " + e.message); return; }
    if (run !== attempt) return; // a newer Reload took over
    if (app && app.running) {
      open.href = app.url;
      open.hidden = trail.hidden;
      sub.textContent = "· sessions from your coding agents";
      const frame = el("iframe", "trail-frame");
      frame.title = "Agent Trail sessions";
      frame.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms allow-popups"); // other origin; no top navigation
      frame.src = app.url;
      trail.replaceChildren(frame);
      return;
    }
    const tool = state.tools.find((t) => t.id === "trail");
    if (!app) return help("Agent Trail is not installed. Install it, then restart the harness:", "npm install -g @camtrik/agent-trail");
    if (tool && tool.status === "error") return help("Agent Trail failed to start: " + (tool.error || "unknown error") + ". See .harness/logs/trail.log, then restart the harness.");
    if (!tool) return help("Agent Trail isn't started by this configuration. Start it by hand:", "HOSTNAME=127.0.0.1 agent-trail");
    if (++tries > 30) return help("Agent Trail did not respond. See .harness/logs/trail.log, then press Reload.");
    help("Starting Agent Trail…");
    setTimeout(() => { if (run === attempt) loadTrail(run); }, 2000);
  }
  const start = () => { tries = 0; loadTrail(++attempt); };

  function show(view) {
    const isTrail = view === "trail";
    trailBtn.classList.toggle("active", isTrail);
    liveBtn.classList.toggle("active", !isTrail);
    trailBtn.setAttribute("aria-pressed", String(isTrail));
    liveBtn.setAttribute("aria-pressed", String(!isTrail));
    trail.hidden = !isTrail;
    list.hidden = isTrail;
    reload.hidden = !isTrail;
    open.hidden = !isTrail || !open.href;
  }
  trailBtn.onclick = () => show("trail");
  liveBtn.onclick = () => show("live");
  reload.onclick = start;
  show("trail");
  start();
}

// ---- explorer (agent chat | editor; run console below) ----

async function renderExplorer() {
  const pane = document.getElementById("pane-explorer");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";
  if (window.Explorer && window.Explorer.init) window.Explorer.init(pane);
  else pane.appendChild(el("p", "hint", "Explorer module unavailable"));
}

// ---- settings (provider, key, test, model list) ----

// ---- settings: provider -> API key -> test -> model, plus connected providers ----

async function renderSettings() {
  const pane = document.getElementById("pane-settings");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  try {
    const d = await fetchJSON("/api/llm");
    state.providers = d.providers || [];
    state.llm = d;
  } catch (e) {
    pane.appendChild(el("p", "muted", "LLM API unavailable: " + e.message));
    return;
  }

  const form = el("div", "harness-input-form");
  form.appendChild(el("div", "hif-form-title", "Connect an LLM provider"));
  form.appendChild(el("p", "hint", "Keys are stored locally in .harness/llm.json, one per provider."));

  function step(n, label) {
    const wrap = el("div", "step");
    const head = el("div", "step-head");
    head.appendChild(el("span", "step-num", String(n)));
    head.appendChild(el("span", "hif-label", label));
    wrap.appendChild(head);
    form.appendChild(wrap);
    return wrap;
  }

  // 1. provider
  const s1 = step(1, "Provider");
  const provider = el("select", "hif-select");
  provider.setAttribute("aria-label", "Provider");
  s1.appendChild(provider);

  // 2. API key + test
  const s2 = step(2, "API key");
  const keyRow = el("div", "row");
  const key = el("input", "hif-input");
  key.type = "password";
  key.autocomplete = "off";
  key.setAttribute("aria-label", "API key");
  const testBtn = el("button", "btn secondary", "Test key");
  keyRow.appendChild(key);
  keyRow.appendChild(testBtn);
  s2.appendChild(keyRow);
  const testOut = el("p", "test-out", "");
  testOut.setAttribute("role", "status");
  s2.appendChild(testOut);

  // 3. model (shown once the key works)
  const s3 = step(3, "Model");
  const modelRow = el("div", "row");
  const model = el("select", "hif-select");
  model.setAttribute("aria-label", "Model");
  const save = el("button", "btn", "Save & use");
  modelRow.appendChild(model);
  modelRow.appendChild(save);
  s3.appendChild(modelRow);
  s3.hidden = true;

  pane.appendChild(form);

  // connected providers
  const conn = el("div", "harness-input-form");
  const connHead = el("div", "row");
  connHead.appendChild(el("div", "hif-form-title", "Connected providers"));
  const refreshBtn = el("button", "btn secondary", "Re-test all");
  connHead.appendChild(refreshBtn);
  conn.appendChild(connHead);
  const connList = el("div", "");
  conn.appendChild(connList);
  pane.appendChild(conn);

  pane.appendChild(el("p", "pane-title", "Skill level"));
  pane.appendChild(el("p", "hint", "Use the Skill selector in the top bar to change level at any time."));

  function isSaved(id) { return (state.llm.configured || []).includes(id); }

  function fillProviders() {
    const cur = provider.value || state.llm.provider;
    provider.innerHTML = "";
    for (const p of state.providers) {
      const o = el("option", "", (p.label || p.id) + (isSaved(p.id) ? "  · key saved" : ""));
      o.value = p.id;
      provider.appendChild(o);
    }
    if (cur) provider.value = cur;
  }

  function setStatus(text, cls) {
    testOut.textContent = text;
    testOut.className = "test-out " + (cls || "");
  }

  function resetSteps() {
    s3.hidden = true;
    key.value = "";
    setStatus("");
    key.placeholder = isSaved(provider.value) ? "Key saved. Leave blank to test it, or paste a new one" : "Paste your API key";
  }

  function showModels(list) {
    model.innerHTML = "";
    for (const m of list) { const o = el("option", "", m); o.value = m; model.appendChild(o); }
    if (state.llm.provider === provider.value && list.includes(state.llm.model)) model.value = state.llm.model;
    s3.hidden = false;
  }

  function renderConnected() {
    connList.innerHTML = "";
    if (!state.available.length) {
      connList.appendChild(el("p", "hint", "No providers connected yet."));
      return;
    }
    for (const p of state.available) {
      const row = el("div", "tool-row");
      row.appendChild(el("span", "status-dot " + (p.ok ? "ok" : "err")));
      row.appendChild(el("span", "tool-name", p.label || p.provider));
      if (state.llm.provider === p.provider) row.appendChild(el("span", "badge", "active"));
      row.appendChild(el("span", "tool-status", p.ok ? p.models.length + " models" : p.error));
      connList.appendChild(row);
    }
  }

  provider.onchange = resetSteps;
  key.addEventListener("keydown", (e) => { if (e.key === "Enter") testBtn.click(); });

  testBtn.onclick = async () => {
    if (!key.value && !isSaved(provider.value)) { setStatus("Enter an API key first.", "err"); key.focus(); return; }
    testBtn.disabled = true;
    s3.hidden = true;
    setStatus("Testing…");
    try {
      const d = await fetchJSON("/api/llm/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ provider: provider.value, api_key: key.value }),
      });
      if (!d.ok) { setStatus("✗ " + d.error, "err"); return; }
      const catalog = (state.providers.find((x) => x.id === provider.value) || {}).models || [];
      const list = d.models && d.models.length ? d.models : catalog;
      setStatus("✓ Key works · " + list.length + " models available", "ok");
      showModels(list);
      model.focus();
    } catch (e) {
      setStatus("✗ " + e.message, "err");
    } finally {
      testBtn.disabled = false;
    }
  };

  save.onclick = async () => {
    save.disabled = true;
    try {
      const d = await fetchJSON("/api/llm", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ provider: provider.value, model: model.value, api_key: key.value }),
      });
      state.llm = d;
      fillProviders();
      resetSteps();
      setStatus("✓ Saved. " + d.provider + " / " + d.model + " is now active.", "ok");
      await loadAvailable();
      renderConnected();
    } catch (e) {
      setStatus("✗ Save failed: " + e.message, "err");
    } finally {
      save.disabled = false;
    }
  };

  refreshBtn.onclick = async () => {
    refreshBtn.disabled = true;
    connList.innerHTML = "";
    connList.appendChild(el("p", "hint", "Testing saved keys…"));
    await loadAvailable(true);
    renderConnected();
    refreshBtn.disabled = false;
  };

  fillProviders();
  resetSteps();
  await loadAvailable();
  renderConnected();
}

function field(label, input) {
  const f = el("div", "field");
  f.appendChild(el("label", "", label));
  f.appendChild(input);
  return f;
}

// ---- sidebar: file tree + tokens + terminal ----

// Sidebar: whole panel collapses from the top bar; each section is a <details>. Both remembered.
function initSidebarCollapse() {
  const ws = document.querySelector(".workspace");
  const toggle = document.getElementById("sidebar-toggle");
  const setCollapsed = (c) => {
    ws.classList.toggle("sidebar-collapsed", c);
    toggle.setAttribute("aria-expanded", String(!c));
    localStorage.setItem("sidebar-collapsed", c ? "1" : "");
  };
  setCollapsed(localStorage.getItem("sidebar-collapsed") === "1");
  toggle.onclick = () => setCollapsed(!ws.classList.contains("sidebar-collapsed"));
  document.querySelectorAll(".side-section").forEach((d) => {
    const key = "sidebar-" + d.dataset.key;
    if (localStorage.getItem(key) === "closed") d.open = false;
    d.addEventListener("toggle", () => localStorage.setItem(key, d.open ? "open" : "closed"));
  });
}

function initSidebar() {
  initSidebarCollapse();
  const treeEl = document.getElementById("filetree");
  if (window.FileTree && window.FileTree.init) window.FileTree.init(treeEl, (path, content) => {
    showPane("explorer");
    if (window.Explorer && window.Explorer.open) window.Explorer.open(path, content);
  });
  else treeEl.appendChild(el("p", "hint", "file tree unavailable"));

  document.getElementById("terminal-btn").onclick = async () => {
    const btn = document.getElementById("terminal-btn");
    btn.disabled = true;
    try { await fetchJSON("/api/terminal", { method: "POST" }); btn.textContent = "Opened"; }
    catch (e) { alert("Terminal failed: " + e.message); }
    finally { btn.disabled = false; }
  };

  loadTokens();
}

async function loadTokens() {
  const panel = document.getElementById("tokens-panel");
  const row = (label, value, cls) => {
    const r = el("div", "tok-row");
    r.appendChild(el("span", "", label));
    r.appendChild(el("span", cls || "", value));
    return r;
  };
  try {
    const d = await fetchJSON("/api/telemetry?days=1");
    const k = (n) => (n >= 1000 ? (n / 1000).toFixed(1) + "k" : String(n));
    const rows = [
      row("LLM calls today", String(d.today.calls)),
      row("Tokens today", k(d.today.prompt_tokens + d.today.completion_tokens)),
      row("Errors today", String(d.today.errors), d.today.errors ? "err-text" : "down"),
      row("All-time tokens", k(d.totals.prompt_tokens + d.totals.completion_tokens)),
    ];
    if (d.budget) rows.push(row("Budget spent", "$" + d.budget.spent.toFixed(2) + (d.budget.over ? " ⚠" : "")));
    const more = el("a", "tok-more", "Open Telemetry →");
    more.href = "#";
    more.onclick = (e) => { e.preventDefault(); showPane("telemetry"); };
    panel.replaceChildren(...rows, more);
  } catch (e) {
    panel.replaceChildren(el("p", "hint", "telemetry unavailable"));
  }
}

// ---- events ----

// eventSummary turns LLM events into one readable line; others fall back to raw JSON.
function eventSummary(ev) {
  const d = ev.data || {};
  const who = d.purpose + " · " + d.provider + "/" + d.model;
  const s = (ms) => (ms / 1000).toFixed(1) + "s";
  switch (ev.ev) {
    case "llm-request": return who + " · " + d.prompt_chars + " chars in" + (d.preview ? " · “" + d.preview + "”" : "");
    case "llm-usage": return who + " · " + d.prompt_tokens + " → " + d.completion_tokens + " tokens" + (d.duration_ms ? " · " + s(d.duration_ms) : "");
    case "llm-error": return who + " · " + (d.cancelled ? "stopped by user" : d.error) + " · " + s(d.duration_ms || 0);
    case "llm-key-check": return d.provider + " key " + (d.ok ? "works · " + d.models + " models" : "failed · " + d.error);
    case "review-finished": return d.status + (d.decision ? " · " + d.decision : "") + (d.error ? " · " + d.error : "");
    default: return "";
  }
}

function connectEvents() {
  const es = new EventSource("/events");
  const dot = document.getElementById("conn");
  es.onopen = () => dot.classList.add("ok");
  es.onerror = () => dot.classList.remove("ok");
  es.onmessage = (m) => {
    try {
      const ev = JSON.parse(m.data);
      trackActivity(ev);
      if (ev.ev === "budget-alert" || ev.ev === "cost" || ev.ev === "level") { loadStatus(); loadTokens(); }
    } catch (e) { /* ignore */ }
  };
}

// ---- init ----

function initThemeToggle() {
  document.getElementById("theme-toggle").onclick = () => {
    const next = document.documentElement.dataset.theme === "light" ? "dark" : "light";
    document.documentElement.dataset.theme = next;
    localStorage.setItem("theme", next);
  };
}

async function init() {
  initThemeToggle();
  initOverview();
  document.getElementById("level-select").onchange = async (e) => {
    try { await setLevel(e.target.value); } catch (err) { alert("level change failed: " + err.message); }
  };
  renderTabs();
  await ensureProviders();
  document.getElementById("model-select").onchange = (e) => {
    if (!state.llm || !e.target.value) return;
    [state.llm.provider, state.llm.model] = JSON.parse(e.target.value);
  };
  loadAvailable();
  initSidebar();
  await loadStatus();
  connectEvents();
  setInterval(loadStatus, 3000);
  setInterval(loadTokens, 10000);
  }

init();