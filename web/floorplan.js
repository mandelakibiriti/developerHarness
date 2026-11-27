// Floorplan: interactive map of floorplan/plan.json (components, screens, endpoints,
// build order). Columns by dependency depth (screens/UI left, core right), curved
// edges, pan/zoom, hover highlighting and a detail panel. Uses globals: el.
(function () {
  'use strict';

  const NS = 'http://www.w3.org/2000/svg';
  const NODE_W = 240, NODE_H = 92, COL_GAP = 130, ROW_GAP = 28, PAD = 48;

  function svg(tag, attrs) {
    const n = document.createElementNS(NS, tag);
    for (const k in attrs) n.setAttribute(k, attrs[k]);
    return n;
  }

  // buildModel turns plan.json into positioned nodes and edges.
  function buildModel(plan) {
    const comps = (plan.components || []).filter((c) => c && c.id);
    const byId = new Map();
    const order = plan.build_order || [];
    comps.forEach((c) => byId.set(c.id, { id: c.id, kind: 'module', name: c.name || c.id, description: c.description || '',
      deps: (c.depends_on || []).filter((d) => d !== c.id), step: order.indexOf(c.id) + 1 }));
    // Endpoints aren't tied to a component in plan.json; screens attach to the API-looking module.
    const apis = comps.filter((c) => /\b(api|http|gateway|rest)\b/i.test((c.id + ' ' + (c.name || '')).replace(/[-_]/g, ' ')));
    const api = apis.find((c) => !/auth/i.test(c.id + ' ' + (c.name || ''))) || apis[0]; // an auth gateway only if nothing else
    (plan.screens || []).filter((s) => s && s.id).forEach((s) => {
      const id = 'screen:' + s.id;
      byId.set(id, { id, kind: 'screen', name: s.name || s.id, description: (s.calls || []).length ? 'Calls ' + s.calls.join(', ') : '',
        deps: api ? [api.id] : [], calls: s.calls || [], step: 0 });
    });
    const nodes = [...byId.values()];
    nodes.forEach((n) => { n.deps = n.deps.filter((d) => byId.has(d)); n.usedBy = []; });
    nodes.forEach((n) => n.deps.forEach((d) => byId.get(d).usedBy.push(n.id)));

    // depth = longest dependency chain below a node; cycles count as 0 rather than looping
    const depth = new Map(), visiting = new Set();
    const depthOf = (n) => {
      if (depth.has(n.id)) return depth.get(n.id);
      if (visiting.has(n.id)) return 0;
      visiting.add(n.id);
      const d = n.deps.length ? 1 + Math.max(...n.deps.map((x) => depthOf(byId.get(x)))) : 0;
      visiting.delete(n.id);
      depth.set(n.id, d);
      return d;
    };
    nodes.forEach(depthOf);
    const maxD = Math.max(0, ...depth.values());
    const cols = [];
    nodes.forEach((n) => { n.col = maxD - depth.get(n.id); (cols[n.col] = cols[n.col] || []).push(n); });
    const rank = (n) => (n.kind === 'screen' ? -1 : n.step || 999);
    const tallest = Math.max(...cols.map((c) => (c ? c.length : 0)));
    const worldH = PAD * 2 + tallest * NODE_H + (tallest - 1) * ROW_GAP;
    cols.forEach((col, ci) => {
      if (!col) return;
      col.sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
      const colH = col.length * NODE_H + (col.length - 1) * ROW_GAP;
      col.forEach((n, i) => {
        n.x = PAD + ci * (NODE_W + COL_GAP);
        n.y = (worldH - colH) / 2 + i * (NODE_H + ROW_GAP);
      });
    });
    const edges = [];
    nodes.forEach((n) => n.deps.forEach((d) => edges.push({ from: n, to: byId.get(d), label: n.kind === 'screen' ? n.calls.join(', ') : '' })));
    return { nodes, edges, byId, api, worldW: PAD * 2 + cols.length * NODE_W + (cols.length - 1) * COL_GAP, worldH };
  }

  function edgePath(a, b) {
    const sx = a.x + NODE_W, sy = a.y + NODE_H / 2, tx = b.x, ty = b.y + NODE_H / 2;
    if (tx <= sx) { // same/left column: loop around the bottom
      const by = Math.max(a.y, b.y) + NODE_H + 24;
      return { d: `M${a.x + NODE_W / 2},${a.y + NODE_H} C${a.x + NODE_W / 2},${by} ${b.x + NODE_W / 2},${by} ${b.x + NODE_W / 2},${b.y + NODE_H}`, mx: (a.x + b.x) / 2 + NODE_W / 2, my: by };
    }
    const dx = (tx - sx) / 2;
    // label sits a third of the way along, near its source, so labels on converging edges don't stack
    const t = 0.3, u = 1 - t;
    const mx = u * u * u * sx + 3 * u * u * t * (sx + dx) + 3 * u * t * t * (tx - dx) + t * t * t * tx;
    const my = u * u * u * sy + 3 * u * u * t * sy + 3 * u * t * t * ty + t * t * t * ty;
    return { d: `M${sx},${sy} C${sx + dx},${sy} ${tx - dx},${ty} ${tx},${ty}`, mx, my };
  }

  function render(container, plan, opts) {
    opts = opts || {};
    container.innerHTML = '';
    const m = buildModel(plan);
    if (!m.nodes.length) { container.appendChild(el('p', 'hint', 'The blueprint has no components yet.')); return null; }
    const order = (plan.build_order || []).map((id) => m.byId.get(id)).filter(Boolean);

    // ---- overview header ----
    const head = el('div', 'fp-head');
    head.appendChild(el('div', 'fp-kicker', 'Blueprint'));
    head.appendChild(el('h3', 'fp-goal', plan.goal || 'Untitled blueprint'));
    const counts = el('div', 'fp-counts');
    const n = (k) => m.nodes.filter((x) => x.kind === k).length;
    counts.textContent = n('module') + ' modules · ' + (plan.endpoints || []).length + ' endpoints · ' + n('screen') + ' screens';
    head.appendChild(counts);
    if (order.length) {
      const steps = el('ol', 'fp-steps');
      steps.setAttribute('aria-label', 'Build order');
      order.forEach((node, i) => {
        const li = el('li', '');
        const b = el('button', 'fp-step', '');
        b.appendChild(el('span', 'fp-step-n', String(i + 1)));
        b.appendChild(document.createTextNode(node.name));
        b.onclick = () => select(node, true);
        li.appendChild(b);
        steps.appendChild(li);
      });
      if (order.length > 12) { // long lists would push the map below the fold
        const more = el('details', 'fp-order');
        more.appendChild(el('summary', '', 'Build order · ' + order.length + ' steps'));
        more.appendChild(steps);
        head.appendChild(more);
      } else head.appendChild(steps);
    }
    container.appendChild(head);

    // ---- canvas ----
    const canvas = el('div', 'fp-canvas');
    canvas.tabIndex = 0;
    canvas.setAttribute('aria-label', 'Blueprint map. Drag to pan, Ctrl or Cmd plus scroll to zoom.');
    const world = el('div', 'fp-world');
    world.style.width = m.worldW + 'px';
    world.style.height = m.worldH + 'px';
    const s = svg('svg', { width: m.worldW, height: m.worldH, class: 'fp-edges' });
    const defs = svg('defs', {});
    const marker = svg('marker', { id: 'fp-arrow', viewBox: '0 0 10 10', refX: 9, refY: 5, markerWidth: 7, markerHeight: 7, orient: 'auto-start-reverse' });
    marker.appendChild(svg('path', { d: 'M0,0 L10,5 L0,10 z', class: 'fp-arrowhead' }));
    defs.appendChild(marker);
    s.appendChild(defs);
    world.appendChild(s);

    m.edges.forEach((e) => {
      const p = edgePath(e.from, e.to);
      e.el = svg('path', { d: p.d, class: 'fp-edge' + (e.from.kind === 'screen' ? ' calls' : ''), 'marker-end': 'url(#fp-arrow)' });
      s.appendChild(e.el);
      if (e.label) {
        e.labelEl = el('div', 'fp-edge-label', e.label.length > 40 ? e.label.slice(0, 39) + '…' : e.label);
        e.labelEl.title = e.label;
        e.labelEl.style.left = p.mx + 'px';
        e.labelEl.style.top = p.my + 'px';
        world.appendChild(e.labelEl);
      }
    });

    m.nodes.forEach((node) => {
      const card = el('button', 'fp-node ' + node.kind);
      card.type = 'button';
      card.style.left = node.x + 'px';
      card.style.top = node.y + 'px';
      card.style.width = NODE_W + 'px';
      card.style.height = NODE_H + 'px';
      const top = el('div', 'fp-node-top');
      top.appendChild(el('span', 'fp-kind', node.kind === 'screen' ? 'Screen' : node === m.byId.get(m.api && m.api.id) ? 'API' : 'Module'));
      if (node.step) top.appendChild(el('span', 'fp-step-n', String(node.step)));
      card.appendChild(top);
      card.appendChild(el('div', 'fp-name', node.name));
      card.appendChild(el('div', 'fp-desc', node.description));
      card.setAttribute('aria-label', node.name + (node.step ? ', build step ' + node.step : '') + '. ' + node.description);
      card.onclick = () => select(node, false);
      card.onmouseenter = () => highlight(node);
      card.onmouseleave = () => highlight(selected);
      card.onfocus = () => highlight(node);
      node.el = card;
      world.appendChild(card);
    });
    canvas.appendChild(world);

    // ---- controls ----
    const ctrls = el('div', 'fp-ctrls');
    const zoomOut = el('button', 'btn secondary icon', '−');
    const fitBtn = el('button', 'btn secondary icon', '⤢');
    const zoomIn = el('button', 'btn secondary icon', '+');
    zoomOut.setAttribute('aria-label', 'Zoom out');
    fitBtn.setAttribute('aria-label', 'Fit to view');
    zoomIn.setAttribute('aria-label', 'Zoom in');
    [zoomOut, fitBtn, zoomIn].forEach((b) => ctrls.appendChild(b));
    canvas.appendChild(ctrls);

    // ---- detail panel ----
    const panel = el('aside', 'fp-panel');
    panel.hidden = true;
    canvas.appendChild(panel);
    container.appendChild(canvas);

    // ---- pan / zoom ----
    const view = { x: 0, y: 0, k: 1 };
    const apply = () => { world.style.transform = `translate(${view.x}px,${view.y}px) scale(${view.k})`; };
    const clampK = (k) => Math.min(2, Math.max(0.3, k));
    function zoomAt(k, cx, cy) {
      k = clampK(k);
      view.x = cx - (cx - view.x) * (k / view.k);
      view.y = cy - (cy - view.y) * (k / view.k);
      view.k = k;
      apply();
    }
    function fit() {
      const r = canvas.getBoundingClientRect();
      const room = r.width - (panel.hidden ? 0 : panel.offsetWidth + 24);
      view.k = clampK(Math.min(room / m.worldW, r.height / m.worldH, 1.1));
      view.x = (room - m.worldW * view.k) / 2;
      view.y = (r.height - m.worldH * view.k) / 2;
      apply();
    }
    zoomIn.onclick = () => { const r = canvas.getBoundingClientRect(); zoomAt(view.k * 1.2, r.width / 2, r.height / 2); };
    zoomOut.onclick = () => { const r = canvas.getBoundingClientRect(); zoomAt(view.k / 1.2, r.width / 2, r.height / 2); };
    fitBtn.onclick = fit;
    canvas.addEventListener('wheel', (e) => {
      const r = canvas.getBoundingClientRect();
      e.preventDefault();
      if (e.ctrlKey || e.metaKey) { // pinch or ⌘/Ctrl+scroll zooms; plain scroll pans, like design canvases
        zoomAt(view.k * Math.exp(-e.deltaY * 0.01), e.clientX - r.left, e.clientY - r.top);
      } else {
        view.x -= e.deltaX;
        view.y -= e.deltaY;
        apply();
      }
    }, { passive: false });
    let drag = null;
    canvas.addEventListener('pointerdown', (e) => {
      if (e.target.closest('.fp-node, .fp-panel, .fp-ctrls')) return;
      drag = { x: e.clientX - view.x, y: e.clientY - view.y };
      canvas.setPointerCapture(e.pointerId);
      canvas.classList.add('dragging');
    });
    canvas.addEventListener('pointermove', (e) => { if (drag) { view.x = e.clientX - drag.x; view.y = e.clientY - drag.y; apply(); } });
    const endDrag = () => { drag = null; canvas.classList.remove('dragging'); };
    canvas.addEventListener('pointerup', endDrag);
    canvas.addEventListener('pointercancel', endDrag);
    // Escape closes the panel even when focus left the canvas (e.g. a re-rendered chip)
    const onEsc = (e) => {
      if (!container.isConnected) { document.removeEventListener('keydown', onEsc); return; }
      if (e.key === 'Escape' && !panel.hidden && container.offsetParent) select(null);
    };
    document.addEventListener('keydown', onEsc);
    canvas.addEventListener('keydown', (e) => {
      if (e.key === '+' || e.key === '=') zoomIn.click();
      if (e.key === '-') zoomOut.click();
      if (e.key === '0') fit();
    });

    // ---- highlight + selection ----
    let selected = null;
    function highlight(node) {
      canvas.classList.toggle('focusing', !!node);
      const near = new Set(node ? [node.id, ...node.deps, ...node.usedBy] : []);
      m.nodes.forEach((x) => x.el.classList.toggle('near', near.has(x.id)));
      m.edges.forEach((e) => {
        const on = !!node && (e.from === node || e.to === node);
        e.el.classList.toggle('hl', on);
        if (e.labelEl) e.labelEl.classList.toggle('hl', on);
      });
    }

    function chips(title, ids) {
      const sec = el('div', 'fp-sec');
      sec.appendChild(el('div', 'fp-sec-title', title + ' ' + ids.length));
      const list = el('div', 'fp-chips');
      ids.forEach((id) => {
        const n2 = m.byId.get(id);
        const b = el('button', 'fp-chip', n2.name);
        b.onclick = () => select(n2, true);
        list.appendChild(b);
      });
      sec.appendChild(list);
      return sec;
    }

    function select(node, center) {
      if (selected) selected.el.classList.remove('selected');
      selected = node;
      highlight(node);
      if (opts.onSelect) opts.onSelect(node);
      if (!node) {
        const wasOpen = !panel.hidden;
        panel.hidden = true;
        if (wasOpen) canvas.focus({ preventScroll: true });
        return;
      }
      node.el.classList.add('selected');
      panel.innerHTML = '';
      const close = el('button', 'fp-close', '×');
      close.setAttribute('aria-label', 'Close details');
      close.onclick = () => select(null);
      panel.appendChild(close);
      const kind = node.kind === 'screen' ? 'Screen' : 'Module' + (node.step ? ' · build step ' + node.step + ' of ' + order.length : '');
      panel.appendChild(el('div', 'fp-kicker', kind));
      panel.appendChild(el('h4', 'fp-panel-name', node.name));
      if (node.description) panel.appendChild(el('p', 'fp-panel-desc', node.description));
      if (node.kind === 'module') panel.appendChild(el('code', 'fp-id', node.id));
      if (node.deps.length) panel.appendChild(chips(node.kind === 'screen' ? 'Talks to' : 'Depends on', node.deps));
      if (node.usedBy.length) panel.appendChild(chips('Used by', node.usedBy));
      const eps = node.kind === 'screen' ? (plan.endpoints || []).filter((ep) => node.calls.includes(ep.path))
        : node.id === (m.api && m.api.id) ? plan.endpoints || [] : [];
      if (eps.length) {
        const sec = el('div', 'fp-sec');
        sec.appendChild(el('div', 'fp-sec-title', 'Endpoints ' + eps.length));
        eps.forEach((ep) => {
          const row = el('div', 'fp-ep');
          row.appendChild(el('span', 'fp-method ' + String(ep.method || '').toLowerCase(), ep.method || ''));
          row.appendChild(el('code', '', ep.path || ''));
          if (ep.purpose) row.appendChild(el('div', 'fp-ep-purpose', ep.purpose));
          sec.appendChild(row);
        });
        panel.appendChild(sec);
      }
      panel.hidden = false;
      close.focus({ preventScroll: true }); // keyboard users land in the details
      if (center) {
        const r = canvas.getBoundingClientRect();
        view.k = Math.max(view.k, 0.9); // a big map fits at a tiny scale; jump in far enough to read
        view.x = (r.width - panel.offsetWidth - 24) / 2 - (node.x + NODE_W / 2) * view.k;
        view.y = r.height / 2 - (node.y + NODE_H / 2) * view.k;
        apply();
      }
    }

    requestAnimationFrame(fit);
    return { select: (id) => select(m.byId.get(id) || null, true), fit };
  }

  window.Floorplan = { render, buildModel };
})();
