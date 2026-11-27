// Telemetry tab: project-level LLM usage from /api/telemetry (built from the
// project's own event log). Uses globals from shell.js: el, fetchJSON.
(function () {
  'use strict';

  const NS = 'http://www.w3.org/2000/svg';
  const fmt = (n) => (n >= 1e6 ? (n / 1e6).toFixed(1) + 'M' : n >= 1e3 ? (n / 1e3).toFixed(1) + 'k' : String(n || 0));
  const secs = (ms) => (ms ? (ms >= 10000 ? Math.round(ms / 1000) + 's' : (ms / 1000).toFixed(1) + 's') : '–');
  const usd = (v) => (v ? '$' + v.toFixed(v < 1 ? 4 : 2) : '–');
  const dayLabel = (iso) => new Date(iso + 'T12:00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  const timeLabel = (t) => new Date(t).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });

  function svg(tag, attrs) {
    const n = document.createElementNS(NS, tag);
    for (const k in attrs) n.setAttribute(k, attrs[k]);
    return n;
  }

  function niceMax(v) {
    const p = Math.pow(10, Math.floor(Math.log10(v)));
    for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
    return 10 * p;
  }

  function statCard(title, value, sub) {
    const c = el('div', 'harness-metric-card');
    c.appendChild(el('div', 'mc-title', title));
    c.appendChild(el('div', 'mc-value', value));
    c.appendChild(el('div', 'mc-status status-info', sub));
    return c;
  }

  function card(title, sub) {
    const c = el('section', 'tele-card');
    const h = el('div', 'tele-head');
    h.appendChild(el('span', 'tele-title', title));
    if (sub) h.appendChild(el('span', 'hint', sub));
    c.appendChild(h);
    return c;
  }

  function table(cols, rows) {
    const t = el('table', 'tele-table');
    const hr = el('tr', '');
    cols.forEach((c) => { const th = el('th', c.num ? 'num' : '', c.label); th.scope = 'col'; hr.appendChild(th); });
    const thead = el('thead', '');
    thead.appendChild(hr);
    t.appendChild(thead);
    const tb = el('tbody', '');
    for (const r of rows) {
      const tr = el('tr', '');
      cols.forEach((c) => {
        const td = el('td', c.num ? 'num' : '');
        const v = c.get(r);
        if (v instanceof Node) td.appendChild(v); else td.textContent = v;
        tr.appendChild(td);
      });
      tb.appendChild(tr);
    }
    t.appendChild(tb);
    return t;
  }

  // Single-series column chart: tokens per day. One hue, so the title names it (no legend);
  // hover/focus shows the in/out split; the table view below carries the same numbers.
  function tokenChart(daily, width) {
    const wrap = el('div', 'chart');
    const vals = daily.map((d) => d.prompt_tokens + d.completion_tokens);
    const max = Math.max(0, ...vals);
    if (!max) { wrap.appendChild(el('p', 'hint', 'No LLM calls in this window yet.')); return wrap; }
    // drawn at the card's pixel width (not viewBox-scaled) so text stays 11px
    const W = Math.max(360, width), H = 200, L = 44, R = 8, T = 10, B = 24;
    const top = niceMax(max), plotH = H - T - B, band = (W - L - R) / daily.length;
    const barW = Math.min(24, band * 0.6);
    const s = svg('svg', { width: W, height: H, viewBox: `0 0 ${W} ${H}`, role: 'img', 'aria-label': 'Tokens per day' });
    for (const f of [0, 0.5, 1]) {
      const y = T + plotH * (1 - f);
      s.appendChild(svg('line', { x1: L, x2: W - R, y1: y, y2: y, class: f ? 'grid' : 'axis' }));
      const lbl = svg('text', { x: L - 8, y: y + 4, 'text-anchor': 'end', class: 'tick' });
      lbl.textContent = fmt(top * f);
      s.appendChild(lbl);
    }
    const every = Math.ceil(daily.length / 7);
    const tip = el('div', 'chart-tip');
    tip.hidden = true;
    daily.forEach((d, i) => {
      const x = L + band * i + (band - barW) / 2, base = T + plotH;
      const h = (vals[i] / top) * plotH;
      if (h > 0) {
        const r = Math.min(4, h, barW / 2), y = base - h;
        s.appendChild(svg('path', { class: 'bar', d: `M${x},${base}V${y + r}Q${x},${y} ${x + r},${y}H${x + barW - r}Q${x + barW},${y} ${x + barW},${y + r}V${base}Z` }));
      }
      if (i % every === 0 || i === daily.length - 1) {
        const t = svg('text', { x: x + barW / 2, y: H - 6, 'text-anchor': 'middle', class: 'tick' });
        t.textContent = dayLabel(d.date);
        s.appendChild(t);
      }
      // hit target spans the whole band, larger than the mark
      const hit = svg('rect', { x: L + band * i, y: T, width: band, height: plotH, class: 'hit', tabindex: 0,
        'aria-label': `${dayLabel(d.date)}: ${vals[i]} tokens (${d.prompt_tokens} in, ${d.completion_tokens} out), ${d.calls} calls` });
      const show = () => {
        tip.innerHTML = '';
        tip.appendChild(el('strong', '', dayLabel(d.date)));
        tip.appendChild(el('div', '', fmt(vals[i]) + ' tokens · ' + d.calls + (d.calls === 1 ? ' call' : ' calls')));
        tip.appendChild(el('div', 'muted', fmt(d.prompt_tokens) + ' in · ' + fmt(d.completion_tokens) + ' out' + (d.errors ? ' · ' + d.errors + ' errors' : '')));
        tip.hidden = false;
        // sit beside the bar, flipping left on the right half so it never covers the mark
        const cx = L + band * (i + 0.5);
        tip.style.left = (i < daily.length / 2 ? cx + barW : cx - barW) + 'px';
        tip.style.transform = i < daily.length / 2 ? 'none' : 'translateX(-100%)';
      };
      hit.addEventListener('mouseenter', show);
      hit.addEventListener('focus', show);
      hit.addEventListener('mouseleave', () => { tip.hidden = true; });
      hit.addEventListener('blur', () => { tip.hidden = true; });
      s.insertBefore(hit, s.firstChild); // under the bar so the highlight never washes it out
    });
    wrap.appendChild(s);
    wrap.appendChild(tip);
    return wrap;
  }

  function statusCell(r) {
    const span = el('span', 'call-status ' + r.status);
    span.appendChild(el('span', 'status-dot ' + (r.status === 'ok' ? 'ok' : r.status === 'error' ? 'err' : '')));
    span.appendChild(document.createTextNode(r.status));
    return span;
  }

  function aggTable(rows, nameLabel) {
    if (!rows.length) return el('p', 'hint', 'No calls yet.');
    return table([
      { label: nameLabel, get: (r) => r.name || '(unknown)' },
      { label: 'Calls', num: true, get: (r) => r.calls },
      { label: 'Errors', num: true, get: (r) => r.errors || '–' },
      { label: 'In', num: true, get: (r) => fmt(r.prompt_tokens) },
      { label: 'Out', num: true, get: (r) => fmt(r.completion_tokens) },
      { label: 'Avg time', num: true, get: (r) => secs(r.avg_latency_ms) },
      { label: 'Cost', num: true, get: (r) => usd(r.cost_usd) },
    ], rows);
  }

  async function render(pane, days) {
    let d;
    try { d = await fetchJSON('/api/telemetry?days=' + days); }
    catch (e) { pane.querySelector('.tele-body').replaceChildren(el('p', 'muted', 'Telemetry unavailable: ' + e.message)); return; }
    const body = el('div', 'tele-body');
    const t = d.totals;

    const cards = el('div', 'metric-cards');
    cards.appendChild(statCard('LLM calls', fmt(t.calls), d.today.calls + ' today'));
    cards.appendChild(statCard('Tokens in / out', fmt(t.prompt_tokens) + ' / ' + fmt(t.completion_tokens), fmt(d.today.prompt_tokens + d.today.completion_tokens) + ' today'));
    cards.appendChild(statCard('Estimated cost', usd(t.cost_usd), t.cost_usd ? 'priced calls only' : 'set prices in llm.json to track'));
    cards.appendChild(statCard('Avg response time', secs(t.avg_latency_ms), 'per completed call'));
    cards.appendChild(statCard('Errors', String(t.errors), d.cancelled + ' stopped by you'));
    body.appendChild(cards);

    const chartCard = card('Tokens per day', 'last ' + d.window_days + ' days · hover a day for the in/out split');
    chartCard.appendChild(tokenChart(d.daily, pane.clientWidth - 34));
    const details = el('details', 'table-view');
    details.appendChild(el('summary', '', 'Table view'));
    details.appendChild(table([
      { label: 'Day', get: (r) => dayLabel(r.date) },
      { label: 'Calls', num: true, get: (r) => r.calls },
      { label: 'Errors', num: true, get: (r) => r.errors },
      { label: 'Tokens in', num: true, get: (r) => r.prompt_tokens },
      { label: 'Tokens out', num: true, get: (r) => r.completion_tokens },
    ], d.daily.slice().reverse()));
    chartCard.appendChild(details);
    body.appendChild(chartCard);

    const split = el('div', 'tele-split');
    const f = card('By feature', 'which part of the harness used the LLM');
    f.appendChild(aggTable(d.by_feature, 'Feature'));
    const m = card('By model', 'provider / model');
    m.appendChild(aggTable(d.by_model, 'Model'));
    split.appendChild(f);
    split.appendChild(m);
    body.appendChild(split);

    const rc = card('Recent LLM calls', 'newest first · every call is also in the Overview timeline');
    rc.appendChild(d.recent.length ? table([
      { label: 'When', get: (r) => timeLabel(r.t) },
      { label: 'Feature', get: (r) => r.purpose },
      { label: 'Model', get: (r) => r.model },
      { label: 'Status', get: statusCell },
      { label: 'Tokens', num: true, get: (r) => (r.prompt_tokens || r.completion_tokens ? fmt(r.prompt_tokens) + ' → ' + fmt(r.completion_tokens) : '–') },
      { label: 'Time', num: true, get: (r) => secs(r.duration_ms) },
      { label: 'Asked / error', get: (r) => { const s = el('span', r.error ? 'err-text' : 'muted', r.error || r.preview || ''); s.title = r.error || r.preview || ''; return s; } },
    ], d.recent) : el('p', 'hint', 'No calls yet.'));
    body.appendChild(rc);

    const act = card('Project activity');
    const a = d.activity || {};
    const row = el('div', 'activity-row');
    for (const [k, label] of [['spec-generated', 'specs generated'], ['doc-write', 'doc edits'], ['review-finished', 'reviews'], ['run', 'code runs'], ['floorplan-generated', 'blueprints']]) {
      const item = el('div', 'activity-item');
      item.appendChild(el('strong', '', String(a[k] || 0)));
      item.appendChild(el('span', 'muted', label));
      row.appendChild(item);
    }
    act.appendChild(row);
    body.appendChild(act);

    pane.querySelector('.tele-body').replaceWith(body);
  }

  function init(pane) {
    pane.innerHTML = '';
    const bar = el('div', 'tele-bar');
    bar.appendChild(el('h2', 'tele-h', 'LLM usage · this project'));
    bar.appendChild(el('span', 'spacer'));
    const range = el('select', '');
    range.setAttribute('aria-label', 'Time window');
    for (const n of [7, 14, 30]) { const o = el('option', '', 'Last ' + n + ' days'); o.value = n; range.appendChild(o); }
    range.value = '14';
    const refresh = el('button', 'btn secondary', 'Refresh');
    bar.appendChild(range);
    bar.appendChild(refresh);
    pane.appendChild(bar);
    pane.appendChild(el('div', 'tele-body'));
    const go = () => render(pane, range.value);
    range.onchange = go;
    refresh.onclick = go;
    return go;
  }

  window.UI = { card, table, statCard }; // shared with evals.js

  let refreshFn = null;
  window.Telemetry = {
    show(pane) { if (!refreshFn) refreshFn = init(pane); refreshFn(); },
  };
})();
