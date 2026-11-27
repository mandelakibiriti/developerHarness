// Evals tab: eval runs and their outcomes, harness versions, proposals and
// everyday signals, from /api/evals. Runs are executed with `harness improve`.
// Uses globals: el, fetchJSON, UI (telemetry.js).
(function () {
  'use strict';

  const when = (t) => (t ? new Date(t).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '–');
  const LABEL = { pass: 'Pass', fail: 'Fail', unscored: 'Unscored', open: 'No result yet' };
  const DOT = { pass: 'ok', fail: 'err', unscored: 'warn', open: 'idle' };

  function outcome(r) {
    const s = el('span', 'call-status');
    s.appendChild(el('span', 'status-dot ' + DOT[r.outcome]));
    s.appendChild(document.createTextNode(LABEL[r.outcome]));
    return s;
  }

  function section(title, sub, rows, cols, empty) {
    const c = UI.card(title, sub);
    c.appendChild(rows.length ? UI.table(cols, rows) : el('p', 'hint', empty));
    return c;
  }

  const code = (text) => el('code', 'eval-code', text);

  function guide() {
    const c = UI.card('How the harness improves itself', 'you approve every change');
    const ol = el('ol', 'eval-steps');
    for (const [title, text, cmd] of [
      ['Run each eval', 'The agent does the task; the harness runs the task’s own test to decide pass or fail.', 'harness improve begin --task <id>  →  run the agent with HARNESS_RUN  →  harness improve end --run <id>'],
      ['Propose', 'The proposer reads the scores and suggests edits to learnable files only.', 'harness improve propose'],
      ['Apply and re-run', 'Apply one candidate on a branch, then run the evals again under the new version.', 'harness improve apply --base <version> --cand <id>'],
      ['Adopt or discard', 'Keep it only if the held-out test tasks improve with nothing newly failing and cost within 10%.', 'harness improve report --base <old> --cand <new> --split test'],
    ]) {
      const li = el('li', '');
      li.appendChild(el('strong', '', title));
      li.appendChild(el('span', 'muted', ' ' + text));
      li.appendChild(code(cmd));
      ol.appendChild(li);
    }
    c.appendChild(ol);
    c.appendChild(UI.table(
      [{ label: 'What gets updated', get: (r) => r[0] }, { label: 'Who may change it', get: (r) => r[1] }],
      [['rules/, tracks/, context/, .claude/skills/ (including scripts under context/)', 'The proposer suggests, you approve'],
        ['context/memory/lessons/', 'You only (denied to the proposer in harness.json)'],
        ['practice-problems/ and evals/', 'You only: the proposer may never edit the tests that score it']]));
    c.appendChild(el('p', 'hint', 'More on learning from developer interactions: evals/README.md.'));
    return c;
  }

  async function render(pane) {
    const body = pane.querySelector('.tele-body');
    let d;
    try { d = await fetchJSON('/api/evals'); }
    catch (e) { body.replaceChildren(el('p', 'muted', 'Evaluations unavailable: ' + e.message)); return; }

    const scored = d.runs.filter((r) => r.outcome === 'pass' || r.outcome === 'fail');
    const passed = scored.filter((r) => r.outcome === 'pass').length;
    const cards = el('div', 'metric-cards');
    cards.appendChild(UI.statCard('Harness version', d.current_version || '–', 'hash of the compiled context'));
    cards.appendChild(UI.statCard('Eval runs', String(d.runs.length), scored.length + ' scored'));
    cards.appendChild(UI.statCard('Pass rate', scored.length ? Math.round(100 * passed / scored.length) + '%' : '–', passed + ' of ' + scored.length + ' scored runs'));
    cards.appendChild(UI.statCard('Proposed edits', String(d.proposals.length), 'recorded; may already be applied'));
    const parts = [cards];
    d.problems.forEach((p) => parts.push(el('p', 'err-text', p)));

    parts.push(section('Evaluation runs', 'newest first · score is out of 100', d.runs, [
      { label: 'Started', get: (r) => when(r.started) },
      { label: 'Run', get: (r) => code(r.run) },
      { label: 'Task', get: (r) => r.task },
      { label: 'Split', get: (r) => r.split || '–' },
      { label: 'Version', get: (r) => code(r.harness_version) },
      { label: 'Outcome', get: outcome },
      { label: 'Score', num: true, get: (r) => (r.scored ? Math.round(r.score) : '–') },
      { label: 'Cost', num: true, get: (r) => (r.cost_usd ? '$' + r.cost_usd.toFixed(4) : '–') },
      { label: 'Why', get: (r) => r.reason || (r.parts ? Object.entries(r.parts).map(([k, v]) => k + ' ' + Math.round(v)).join(' · ') : '') },
    ], 'No evaluation has been run yet. Start with step 1 below.'));

    const verdict = d.verdict ? (d.verdict.propose ? 'Latest change is worth adopting: ' : 'Latest change is not worth adopting: ') + d.verdict.reasons.join('; ') : 'compared once two versions have runs';
    parts.push(section('Versions', verdict, d.versions.slice().reverse(), [
      { label: 'Version', get: (v) => code(v.harness_version) },
      { label: 'Mean score', num: true, get: (v) => Math.round(v.mean) },
      { label: 'Tasks', get: (v) => v.tasks.filter((t) => t.runs).map((t) => t.task + ' ' + Math.round(t.pass_rate * 100) + '% (' + t.runs + ')').join(', ') || '–' },
      { label: 'Not scored', get: (v) => (v.missing || []).join(', ') || '–' },
    ], 'No scored versions yet.'));

    parts.push(section('Eval tasks', 'the test command alone decides pass or fail', d.tasks, [
      { label: 'Task', get: (t) => t.id },
      { label: 'Split', get: (t) => t.split || 'not split' },
      { label: 'Test command', get: (t) => code(t.test) },
      { label: 'Runs', num: true, get: (t) => t.runs },
    ], 'No tasks in evals/tasks.'));

    parts.push(section('Proposed edits', 'recorded by harness improve propose', d.proposals, [
      { label: 'From version', get: (p) => code(p.base) },
      { label: 'Candidate', get: (p) => code(p.id) },
      { label: 'File', get: (p) => p.file },
      { label: 'Why', get: (p) => { const s = el('span', '', p.why); s.title = p.why; return s; } },
    ], 'No proposals yet.'));

    const counts = Object.entries(d.signal_counts).map(([k, n]) => n + ' × ' + k).join(' · ');
    parts.push(section('Signals from everyday use', counts || 'candidates for new rules, lessons and eval tasks', d.signals, [
      { label: 'When', get: (s) => when(s.t) },
      { label: 'Signal', get: (s) => s.kind },
      { label: 'Detail', get: (s) => { const n = el('span', '', s.detail); n.title = s.detail; return n; } },
    ], 'Nothing has gone wrong yet in everyday use.'));
    parts.push(guide());
    body.replaceChildren(...parts);
  }

  window.Evals = {
    show(pane) {
      if (!pane.firstChild) {
        const bar = el('div', 'tele-bar');
        bar.appendChild(el('h2', 'tele-h', 'Evaluations and self-improvement'));
        bar.appendChild(el('span', 'spacer'));
        const refresh = el('button', 'btn secondary', 'Refresh');
        refresh.onclick = () => render(pane);
        bar.appendChild(refresh);
        pane.appendChild(bar);
        pane.appendChild(el('div', 'tele-body'));
      }
      render(pane);
    },
  };
})();
