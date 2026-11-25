"use strict";

// Shared contract for the shell and the swarm modules (filetree.js, visuals.js).
const state = {
  tools: [], profile: "", level: "beginner", gates: [], budget: null, scope: "",
  providers: [], llm: null, liveModels: {},
};

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

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "architect", label: "Architect" },
  { id: "map", label: "Map" },
  { id: "graph", label: "Graph" },
  { id: "build", label: "Build" },
  { id: "explorer", label: "Explorer" },
  { id: "settings", label: "Settings" },
];

// ---- tabs ----

function renderTabs() {
  const bar = document.getElementById("tabs");
  bar.innerHTML = "";
  TABS.forEach((t, i) => {
    const b = el("button", "tab" + (i === 0 ? " active" : ""), t.label);
    b.onclick = () => showPane(t.id, b);
    bar.appendChild(b);
  });
}

function showPane(id, btn) {
  document.querySelectorAll(".pane").forEach((p) => (p.hidden = true));
  document.getElementById("pane-" + id).hidden = false;
  document.querySelectorAll(".tab").forEach((b) => b.classList.remove("active"));
  if (btn) btn.classList.add("active");
  if (id === "architect") renderArchitect();
  if (id === "map") renderMap();
  if (id === "graph") renderGraph();
  if (id === "build") renderBuild();
  if (id === "explorer") renderExplorer();
  if (id === "settings") renderSettings();
}

// ---- status ----

async function loadStatus() {
  try {
    const s = await fetchJSON("/api/status");
    state.profile = s.profile;
    state.level = s.level;
    state.tools = s.tools || [];
    state.gates = (s.track && s.track.gates) || [];
    state.budget = s.budget || null;
    state.scope = s.scope || "";
    document.getElementById("profile").textContent = s.profile || "";
    document.getElementById("level-select").value = s.level || "beginner";
    renderOverview();
  } catch (e) { /* not ready */ }
}

// ---- global model selector (top bar) ----

function renderModelSelector() {
  const provEl = document.getElementById("model-provider");
  const modelEl = document.getElementById("model-select");
  if (!provEl || !modelEl) return;
  provEl.innerHTML = "";
  const def = el("option", "", "(provider)");
  def.value = "";
  provEl.appendChild(def);
  for (const p of state.providers) {
    const o = el("option", "", p.id);
    o.value = p.id;
    provEl.appendChild(o);
  }
  if (state.llm && state.llm.provider) provEl.value = state.llm.provider;
  fillModelSelect(provEl.value);
  if (state.llm && state.llm.model) modelEl.value = state.llm.model;
}

function fillModelSelect(providerId) {
  const modelEl = document.getElementById("model-select");
  if (!modelEl) return;
  modelEl.innerHTML = "";
  const live = (state.liveModels && state.liveModels[providerId]) || null;
  const p = state.providers.find((x) => x.id === providerId);
  const list = live || (p && p.models) || [];
  if (!list.length) {
    const o = el("option", "", "(default)");
    o.value = "";
    modelEl.appendChild(o);
  }
  for (const m of list) {
    const o = el("option", "", m);
    o.value = m;
    modelEl.appendChild(o);
  }
}

async function refreshModels(providerId) {
  const id = providerId || (state.llm && state.llm.provider) || "";
  try {
    const d = await fetchJSON("/api/llm/models?provider=" + encodeURIComponent(id));
    if (d.models && d.models.length) {
      state.liveModels = state.liveModels || {};
      state.liveModels[id] = d.models;
      fillModelSelect(id);
      const modelEl = document.getElementById("model-select");
      if (state.llm && state.llm.model && modelEl) modelEl.value = state.llm.model;
    }
  } catch (e) { /* provider may not expose /models */ }
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
  if (state.budget) {
    const b = state.budget;
    const pct = Math.round((b.ratio || 0) * 100);
    const row = el("div", "meta-row");
    row.appendChild(el("span", "meta-label", "Budget"));
    row.appendChild(el("span", "meta-value" + (b.over ? " over" : ""),
      "$" + b.spent.toFixed(2) + " / $" + b.per_feature_usd.toFixed(2) + " (" + pct + "%)" + (b.over ? " — OVER" : "")));
    ov.appendChild(row);
  }
  if (state.gates.length) {
    const row = el("div", "meta-row");
    row.appendChild(el("span", "meta-label", "Gates"));
    row.appendChild(el("span", "meta-value", state.gates.join(", ")));
    ov.appendChild(row);
  }
  if (state.scope) {
    const row = el("div", "meta-row");
    row.appendChild(el("span", "meta-label", "Scope"));
    row.appendChild(el("span", "meta-value", state.scope));
    ov.appendChild(row);
  }

  ov.appendChild(el("p", "pane-title", "LearnHarness"));
  const learnRow = el("div", "tool-row");
  learnRow.appendChild(el("span", "tool-name", "LearnHarness"));
  const learnStatus = el("span", "tool-status", "checking…");
  const launchBtn = el("button", "btn secondary", "Launch");
  launchBtn.style.display = "none";
  launchBtn.onclick = async () => {
    try { await fetchJSON("/api/learn", { method: "POST" }); learnStatus.textContent = "launching…"; }
    catch (e) { learnStatus.textContent = "launch failed: " + e.message; }
  };
  learnRow.appendChild(learnStatus);
  learnRow.appendChild(launchBtn);
  ov.appendChild(learnRow);
  fetchJSON("/api/learn").then((d) => {
    if (d.installed) { learnStatus.textContent = "installed ✓"; launchBtn.style.display = ""; }
    else learnStatus.textContent = "not installed";
  }).catch(() => (learnStatus.textContent = "unavailable"));

  ov.appendChild(el("p", "pane-title", "Tools"));
  if (!state.tools.length) ov.appendChild(el("p", "muted", "No tools active in this profile."));
  for (const t of state.tools) {
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

// ---- chat component ----

function buildChat(mode) {
  const wrap = el("div", "");
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

  async function sendTurn() {
    const text = input.value.trim();
    if (!text) return;
    input.value = "";
    append("user", text);
    history.push({ role: "user", content: text });
    send.disabled = true;
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

  send.onclick = sendTurn;
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); sendTurn(); }
  });

  return { wrap, append, history, input };
}

// ---- architect ----

async function renderArchitect() {
  const pane = document.getElementById("pane-architect");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  pane.appendChild(el("p", "hint", "Define the architecture and functional requirements. Output lands in /docs."));

  const chat = buildChat("architect");

  const goal = el("input", "", "");
  goal.placeholder = "Project goal, e.g. 'A Todo app: web + API'";
  const genBtn = el("button", "btn", "Generate spec → /docs");

  pane.appendChild(chat.wrap);

  const row = el("div", "chat-actions");
  row.appendChild(goal);
  row.appendChild(genBtn);
  pane.appendChild(row);

  genBtn.onclick = async () => {
    const g = goal.value.trim() || "the project";
    genBtn.disabled = true;
    genBtn.textContent = "Generating…";
    try {
      const m = currentModel();
      const d = await fetchJSON("/api/docs/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ goal: g, level: state.level, provider: m.provider, model: m.model }),
      });
      chat.append("assistant", "Wrote " + d.files.join(", ") + " to " + d.dir);
      loadDocsList();
    } catch (e) {
      chat.append("assistant", "Error: " + e.message);
    } finally {
      genBtn.disabled = false;
      genBtn.textContent = "Generate spec → /docs";
    }
  };

  pane.appendChild(el("p", "pane-title", "Specification documents (/docs)"));
  const docsList = el("ul", "docs-list");
  pane.appendChild(docsList);
  const editor = el("div", "");
  pane.appendChild(editor);

  const reviewBtn = el("button", "btn secondary", "Review with Plannotator");
  const scaleLink = el("a", "hint", "Architecture principles → designed-to-scale.vercel.app");
  scaleLink.href = "https://designed-to-scale.vercel.app/";
  scaleLink.target = "_blank";
  scaleLink.rel = "noopener";
  const bottom = el("div", "chat-actions");
  bottom.appendChild(reviewBtn);
  bottom.appendChild(scaleLink);
  pane.appendChild(bottom);

  reviewBtn.onclick = async () => {
    try {
      const d = await fetchJSON("/api/review", { method: "POST" });
      chat.append("assistant", "Review started. Result lands in " + d.result_file);
    } catch (e) { chat.append("assistant", "Error: " + e.message); }
  };

  window._architectDocs = { list: docsList, editor };
  loadDocsList();
}

async function loadDocsList() {
  const ref = window._architectDocs;
  if (!ref) return;
  const { list, editor } = ref;
  try {
    const d = await fetchJSON("/api/docs");
    const files = d.files || [];
    list.innerHTML = "";
    if (!files.length) list.appendChild(el("li", "", "No documents yet."));
    for (const f of files) {
      const li = el("li", "", f.name);
      li.onclick = () => openDoc(f.name, editor);
      list.appendChild(li);
    }
  } catch (e) {
    list.innerHTML = "";
    list.appendChild(el("li", "", "docs/ not available"));
  }
}

async function openDoc(name, editorEl) {
  const d = await fetchJSON("/api/docs?name=" + encodeURIComponent(name));
  editorEl.innerHTML = "";
  editorEl.appendChild(el("p", "pane-title", name));
  const ta = el("textarea", "", d.content);
  ta.style.cssText = "width:100%;min-height:280px;background:var(--bg);color:var(--fg);border:1px solid var(--border);border-radius:6px;padding:8px;font:inherit;";
  const save = el("button", "btn", "Save");
  const bar = el("div", "chat-actions");
  bar.appendChild(save);
  save.onclick = async () => {
    await fetchJSON("/api/docs?name=" + encodeURIComponent(name), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content: ta.value }),
    });
    loadDocsList();
  };
  editorEl.appendChild(ta);
  editorEl.appendChild(bar);
}

// ---- map (floorplan visual + chat) ----

async function renderMap() {
  const pane = document.getElementById("pane-map");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  pane.appendChild(el("p", "hint", "A Floorplan blueprint derived from the spec, with a production-grade, secure-by-design layout and build order."));

  const goal = el("input", "", "");
  goal.placeholder = "Goal for the blueprint";
  const genBtn = el("button", "btn", "Generate blueprint → floorplan/plan.json");
  const bar = el("div", "chat-actions");
  bar.appendChild(goal);
  bar.appendChild(genBtn);
  pane.appendChild(bar);

  const viz = el("div", "viz");
  pane.appendChild(viz);

  const chat = buildChat("architect");
  pane.appendChild(el("p", "pane-title", "Discuss the blueprint"));
  pane.appendChild(chat.wrap);

  genBtn.onclick = async () => {
    genBtn.disabled = true;
    try {
      const m = currentModel();
      await fetchJSON("/api/floorplan/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ goal: goal.value.trim() || "the project", level: state.level, provider: m.provider, model: m.model }),
      });
      loadFloorplan(viz);
    } catch (e) { viz.textContent = "Error: " + e.message; }
    finally { genBtn.disabled = false; }
  };

  loadFloorplan(viz);
}

async function loadFloorplan(viz) {
  try {
    const d = await fetchJSON("/api/floorplan");
    if (!d.exists) { viz.innerHTML = ""; viz.appendChild(el("p", "hint", d.hint || "No blueprint yet.")); return; }
    if (window.Visuals && window.Visuals.renderFloorplan) window.Visuals.renderFloorplan(viz, d.plan);
    else viz.appendChild(el("pre", "out", JSON.stringify(d.plan, null, 2)));
  } catch (e) { viz.appendChild(el("p", "hint", "Error: " + e.message)); }
}

// ---- graph (visual + traverse + chat) ----

async function renderGraph() {
  const pane = document.getElementById("pane-graph");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  pane.appendChild(el("p", "hint", "A knowledge graph of the codebase. Build it with /graphify in Claude Code, then traverse it here."));

  const status = el("div", "meta-row", "");
  pane.appendChild(status);

  const q = el("input", "", "");
  q.placeholder = "Ask the graph, e.g. 'how does auth flow work?'";
  const qBtn = el("button", "btn", "Traverse");
  const bar = el("div", "chat-actions");
  bar.appendChild(q);
  bar.appendChild(qBtn);
  pane.appendChild(bar);

  const viz = el("div", "viz");
  pane.appendChild(viz);

  const out = el("pre", "out", "");
  pane.appendChild(out);

  const chat = buildChat("general");
  pane.appendChild(el("p", "pane-title", "Discuss the graph"));
  pane.appendChild(chat.wrap);

  async function loadGraph() {
    try {
      const d = await fetchJSON("/api/graph");
      if (d.exists && d.graph) {
        status.textContent = "Graph ready: " + (d.graph.nodes || []).length + " nodes, " + (d.graph.links || []).length + " links";
        if (window.Visuals && window.Visuals.renderGraph) window.Visuals.renderGraph(viz, d.graph);
        else viz.appendChild(el("pre", "out", JSON.stringify(d.graph).slice(0, 2000)));
      } else {
        status.textContent = d.hint || "No graph yet.";
      }
    } catch (e) { status.textContent = "Graph unavailable: " + e.message; }
  }

  qBtn.onclick = async () => {
    const question = q.value.trim();
    if (!question) return;
    qBtn.disabled = true;
    out.textContent = "Traversing…";
    try {
      const d = await fetchJSON("/api/graph/query", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ question }) });
      out.textContent = d.answer;
    } catch (e) { out.textContent = "Error: " + e.message; }
    finally { qBtn.disabled = false; }
  };

  loadGraph();
}

// ---- build ----

async function renderBuild() {
  const pane = document.getElementById("pane-build");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  pane.appendChild(el("p", "hint", "Test-driven building of the architecture components. Guidance adapts to your skill level (top bar): beginner = you code with hints, intermediate = you review, advanced = autonomous with gates."));
  const chat = buildChat("build");
  pane.appendChild(chat.wrap);
}

// ---- explorer (3-grid: tree | chat | file; floorplan below) ----

async function renderExplorer() {
  const pane = document.getElementById("pane-explorer");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";
  if (window.Explorer && window.Explorer.init) window.Explorer.init(pane);
  else pane.appendChild(el("p", "hint", "Explorer module unavailable"));
}

// ---- settings (provider, key, test, model list) ----

async function renderSettings() {
  const pane = document.getElementById("pane-settings");
  if (pane.dataset.rendered) return;
  pane.dataset.rendered = "1";
  pane.innerHTML = "";

  pane.appendChild(el("p", "pane-title", "LLM provider"));
  const provider = el("select", "", "");
  const model = el("select", "", "");
  const key = el("input", "", "");
  key.type = "password";
  key.placeholder = "API key (stored locally in .harness/llm.json)";
  const testBtn = el("button", "btn secondary", "Test & list models");
  const save = el("button", "btn", "Save model & key");
  const testOut = el("p", "hint", "");

  pane.appendChild(field("Provider", provider));
  pane.appendChild(field("Model", model));
  pane.appendChild(field("API key", key));
  const btns = el("div", "chat-actions");
  btns.appendChild(testBtn);
  btns.appendChild(save);
  pane.appendChild(btns);
  pane.appendChild(testOut);

  pane.appendChild(el("p", "pane-title", "Skill level"));
  pane.appendChild(el("p", "hint", "Use the Skill selector in the top bar to change level at any time."));

  try {
    const d = await fetchJSON("/api/llm");
    state.providers = d.providers || [];
    state.llm = d;
  } catch (e) {
    pane.insertBefore(el("p", "muted", "LLM API unavailable: " + e.message), pane.firstChild);
  }

  function fillModels() {
    model.innerHTML = "";
    const p = state.providers.find((x) => x.id === provider.value);
    if (p && p.models) {
      for (const m of p.models) { const o = el("option", "", m); o.value = m; model.appendChild(o); }
    } else {
      const o = el("option", "", "(enter or test to list)");
      o.value = "";
      model.appendChild(o);
    }
  }

  state.providers.forEach((p) => {
    const o = el("option", "", p.id + " — " + p.base_url);
    o.value = p.id;
    provider.appendChild(o);
  });
  if (state.llm && state.llm.provider) provider.value = state.llm.provider;
  fillModels();
  if (state.llm && state.llm.model) model.value = state.llm.model;
  provider.onchange = fillModels;

  testBtn.onclick = async () => {
    testBtn.disabled = true;
    testOut.textContent = "Testing…";
    try {
      const d = await fetchJSON("/api/llm/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ provider: provider.value, model: model.value, api_key: key.value }),
      });
      if (d.ok) {
        testOut.textContent = "OK ✓" + (d.models && d.models.length ? " (" + d.models.length + " models)" : "");
        if (d.models && d.models.length) {
          state.liveModels = state.liveModels || {};
          state.liveModels[provider.value] = d.models;
          model.innerHTML = "";
          for (const m of d.models) { const o = el("option", "", m); o.value = m; model.appendChild(o); }
          renderModelSelector();
        }
      } else {
        testOut.textContent = "Failed: " + d.error;
      }
    } catch (e) {
      testOut.textContent = "Failed: " + e.message;
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
      state.providers = d.providers || state.providers;
      key.value = "";
      save.textContent = d.has_key ? "Saved ✓" : "Saved (no key)";
      renderModelSelector();
      refreshModels(provider.value);
    } catch (e) { alert("Save failed: " + e.message); }
    finally {
      save.disabled = false;
      setTimeout(() => (save.textContent = "Save model & key"), 1500);
    }
  };
}

function field(label, input) {
  const f = el("div", "field");
  f.appendChild(el("label", "", label));
  f.appendChild(input);
  return f;
}

// ---- sidebar: file tree + tokens + terminal ----

function initSidebar() {
  const treeEl = document.getElementById("filetree");
  if (window.FileTree && window.FileTree.init) window.FileTree.init(treeEl);
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
  try {
    const d = await fetchJSON("/api/tokens");
    panel.innerHTML = "";
    const spend = el("div", "tok-row");
    spend.appendChild(el("span", "", "TokenTelemetry"));
    spend.appendChild(el("span", d.spend.available ? "ok" : "down", d.spend.available ? "up" : "down"));
    panel.appendChild(spend);
    const trail = el("div", "tok-row");
    trail.appendChild(el("span", "", "Agent Trail"));
    trail.appendChild(el("span", d.trail.available ? "ok" : "down", d.trail.available ? "up" : "down"));
    panel.appendChild(trail);
    if (d.budget) {
      const b = el("div", "tok-row");
      b.appendChild(el("span", "", "Budget spent"));
      b.appendChild(el("span", "", "$" + d.budget.spent.toFixed(2) + (d.budget.over ? " ⚠" : "")));
      panel.appendChild(b);
    }
  } catch (e) {
    panel.innerHTML = "";
    panel.appendChild(el("p", "hint", "tokens unavailable"));
  }
}

// ---- events ----

function addEvent(ev) {
  const ul = document.getElementById("events");
  if (!ul) return;
  const li = el("li", "event" + (ev.ev === "budget-alert" ? " alert" : ""));
  li.appendChild(el("span", "ev-time", ev.t ? ev.t.slice(11, 19) : ""));
  li.appendChild(el("span", "ev-name", ev.ev + (ev.tool ? " · " + ev.tool : "")));
  if (ev.data && Object.keys(ev.data).length) li.appendChild(el("span", "ev-data", JSON.stringify(ev.data)));
  ul.prepend(li);
  while (ul.children.length > 200) ul.removeChild(ul.lastChild);
}

function connectEvents() {
  const es = new EventSource("/events");
  const dot = document.getElementById("conn");
  es.onopen = () => dot.classList.add("ok");
  es.onerror = () => dot.classList.remove("ok");
  es.onmessage = (m) => {
    try {
      const ev = JSON.parse(m.data);
      addEvent(ev);
      if (ev.ev === "budget-alert" || ev.ev === "cost" || ev.ev === "level") { loadStatus(); loadTokens(); }
    } catch (e) { /* ignore */ }
  };
}

// ---- init ----

async function init() {
  document.getElementById("level-select").onchange = async (e) => {
    try { await setLevel(e.target.value); } catch (err) { alert("level change failed: " + err.message); }
  };
  renderTabs();
  await ensureProviders();
  renderModelSelector();
  const provEl = document.getElementById("model-provider");
  const modelEl = document.getElementById("model-select");
  if (provEl) provEl.onchange = async (e) => {
    if (state.llm) state.llm.provider = e.target.value;
    fillModelSelect(e.target.value);
    if (e.target.value) refreshModels(e.target.value);
  };
  if (modelEl) modelEl.onchange = (e) => { if (state.llm) state.llm.model = e.target.value; };
  initSidebar();
  await loadStatus();
  connectEvents();
  setInterval(loadStatus, 3000);
  setInterval(loadTokens, 10000);
}

init();
