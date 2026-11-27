// Explorer: agent chat (left) | code editor (right), run console across the bottom.
// The chat sees the editor buffer with each turn (only when it changed), so the
// agent can guide, confirm the code will work, or suggest a better approach.
// Uses globals from shell.js: el, fetchJSON, buildChat, state.
(function () {
  'use strict';

  const LANGS = {
    go: { label: 'Go', starter: 'package main\n\nimport "fmt"\n\nfunc main() {\n\tname := "harness"\n\tfmt.Println("hello,", name)\n}\n' },
    python: { label: 'Python', starter: 'name = "harness"\nprint(f"hello, {name}")\n' },
    javascript: { label: 'JavaScript', starter: 'const name = "harness";\nconsole.log(`hello, ${name}`);\n' },
  };
  const EXT_LANG = { go: 'go', py: 'python', js: 'javascript', mjs: 'javascript', cjs: 'javascript' };
  const MAX_CTX_CHARS = 12000; // keep chat context bounded for large files

  let api = null; // set by init; window.Explorer.open forwards to it

  function langFor(path) {
    const ext = (path.split('.').pop() || '').toLowerCase();
    return EXT_LANG[ext] || null;
  }

  function card(cls, title) {
    const c = el('section', 'ide-card ' + cls);
    const head = el('div', 'ide-head');
    head.appendChild(el('span', '', title));
    c.appendChild(head);
    return { card: c, head };
  }

  function buildEditor() {
    const wrap = el('div', 'editor mono');
    const gutter = el('pre', 'gutter', '1');
    gutter.setAttribute('aria-hidden', 'true');
    const ta = el('textarea', '', '');
    ta.spellcheck = false;
    ta.setAttribute('aria-label', 'Code editor');
    wrap.appendChild(gutter);
    wrap.appendChild(ta);

    function syncGutter() {
      const lines = ta.value.split('\n').length;
      let s = '';
      for (let i = 1; i <= lines; i++) s += i + '\n';
      gutter.textContent = s;
      gutter.scrollTop = ta.scrollTop;
    }
    ta.addEventListener('input', syncGutter);
    ta.addEventListener('scroll', () => { gutter.scrollTop = ta.scrollTop; });
    // Tab inserts a tab; Esc then Tab moves focus on, so keyboard users aren't trapped.
    let escaped = false;
    ta.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') { escaped = true; return; }
      const passThrough = escaped || e.key !== 'Tab' || e.shiftKey || e.metaKey || e.ctrlKey;
      escaped = false;
      if (passThrough) return;
      e.preventDefault();
      ta.setRangeText('\t', ta.selectionStart, ta.selectionEnd, 'end');
      syncGutter();
    });

    return {
      wrap, ta,
      get value() { return ta.value; },
      set value(v) { ta.value = v; ta.scrollTop = 0; syncGutter(); },
    };
  }

  function init(pane) {
    pane.innerHTML = '';
    const grid = el('div', 'ide');
    let fileName = 'scratch';
    let lastRun = null; // { lang, output, exit_code, timed_out }

    // ---- editor (right) ----
    const ed = card('ide-editor', 'Editor');
    const fileLabel = el('span', 'sub mono', fileName);
    const langSel = el('select', '', '');
    langSel.setAttribute('aria-label', 'Language');
    for (const [id, l] of Object.entries(LANGS)) {
      const o = el('option', '', l.label);
      o.value = id;
      langSel.appendChild(o);
    }
    const resetBtn = el('button', 'btn secondary', 'Reset');
    resetBtn.title = 'Replace the buffer with the starter for this language';
    const runBtn = el('button', 'btn', '▶ Run');
    runBtn.title = 'Run (⌘/Ctrl + Enter)';
    ed.head.appendChild(fileLabel);
    ed.head.appendChild(el('span', 'spacer'));
    ed.head.appendChild(langSel);
    ed.head.appendChild(resetBtn);
    ed.head.appendChild(runBtn);
    const editor = buildEditor();
    ed.card.appendChild(editor.wrap);

    // ---- chat (left) ----
    const ch = card('ide-chat', 'Agent');
    ch.head.appendChild(el('span', 'sub', '· guidance adapts to your skill level'));
    const chat = buildChat('build', () => {
      const code = editor.value.slice(0, MAX_CTX_CHARS);
      let ctx = 'Current editor (' + fileName + ', ' + langSel.value + '):\n```' + langSel.value + '\n' + code + '\n```';
      if (lastRun) ctx += '\nLast run (exit ' + lastRun.exit_code + (lastRun.timed_out ? ', timed out' : '') + '):\n```\n' + lastRun.output.slice(0, 4000) + '\n```';
      return ctx;
    });
    chat.input.placeholder = 'Ask the agent about your code… (Enter to send)';
    ch.card.appendChild(chat.wrap);
    chat.append('assistant', 'Write or open code on the right (click a file in the sidebar), run it below, and ask me anything. I can see the editor and the last run output.');

    // ---- console (bottom) ----
    const co = card('ide-console', 'Output');
    const status = el('span', 'run-status', '');
    const checkBtn = el('button', 'btn secondary', 'Will this work?');
    const bestBtn = el('button', 'btn secondary', 'Suggest a better approach');
    const clearBtn = el('button', 'btn secondary', 'Clear');
    co.head.appendChild(status);
    co.head.appendChild(el('span', 'spacer'));
    co.head.appendChild(checkBtn);
    co.head.appendChild(bestBtn);
    co.head.appendChild(clearBtn);
    const out = el('pre', 'console-out mono', '');
    out.setAttribute('role', 'log');
    out.setAttribute('aria-live', 'polite');
    co.card.appendChild(out);

    grid.appendChild(ch.card);
    grid.appendChild(ed.card);
    grid.appendChild(co.card);
    pane.appendChild(grid);

    // ---- behaviour ----
    async function run() {
      if (runBtn.disabled) return;
      runBtn.disabled = true;
      status.className = 'run-status';
      status.textContent = 'Running…';
      out.textContent = '';
      try {
        const d = await fetchJSON('/api/run', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ lang: langSel.value, code: editor.value }),
        });
        lastRun = { lang: langSel.value, output: d.output || '', exit_code: d.exit_code, timed_out: d.timed_out };
        out.textContent = (d.output || '(no output)') + (d.truncated ? '\n… output truncated' : '');
        const ok = d.exit_code === 0 && !d.timed_out;
        status.className = 'run-status ' + (ok ? 'ok' : 'err');
        status.textContent = (d.timed_out ? 'Timed out' : 'Exit ' + d.exit_code) + ' · ' + d.duration_ms + ' ms';
      } catch (e) {
        lastRun = null;
        status.className = 'run-status err';
        status.textContent = 'Could not run';
        out.textContent = e.message;
      } finally {
        runBtn.disabled = false;
      }
    }

    function setLang(lang, withStarter) {
      langSel.value = lang;
      if (withStarter) editor.value = LANGS[lang].starter;
    }

    runBtn.onclick = run;
    resetBtn.onclick = () => {
      if (editor.value.trim() && !confirm('Replace the editor contents with the starter code?')) return;
      fileName = 'scratch';
      fileLabel.textContent = fileName;
      setLang(langSel.value, true);
    };
    langSel.onchange = () => {
      // swap in the starter only when the buffer is still an untouched starter
      const untouched = Object.values(LANGS).some((l) => l.starter === editor.value) || !editor.value.trim();
      if (untouched) setLang(langSel.value, true);
    };
    editor.ta.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); run(); }
    });
    checkBtn.onclick = () => chat.ask(lastRun
      ? 'I ran this code. Does it do what it should, and will it hold up? Point out bugs or edge cases.'
      : 'Will this code work as written? Walk through it and point out any bugs or edge cases before I run it.');
    bestBtn.onclick = () => chat.ask('Is there a better or more idiomatic way to write this? Recommend the best approach and explain the trade-offs.');
    clearBtn.onclick = () => { out.textContent = ''; status.textContent = ''; lastRun = null; };

    setLang('go', true);

    api = {
      open(path, content) {
        fileName = path;
        fileLabel.textContent = path;
        fileLabel.title = path;
        const lang = langFor(path);
        if (lang) langSel.value = lang;
        editor.value = content;
        lastRun = null;
        status.textContent = lang ? '' : 'Run supports Go, Python and JavaScript';
        status.className = 'run-status';
      },
    };
  }

  window.Explorer = {
    init,
    open(path, content) { if (api) api.open(path, content); },
  };
})();
