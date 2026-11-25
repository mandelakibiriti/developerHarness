(function () {
  'use strict';

  function fail(el, msg) {
    if (el) {
      el.appendChild(window.H.el('div', 'hint', msg));
    }
  }

  window.Explorer = {
    init(containerEl) {
      if (!containerEl) return;
      containerEl.innerHTML = '';
      try {
        if (!window.H || !window.H.el) {
          fail(containerEl, 'H helper not loaded.');
          return;
        }
      } catch (e) {
        return;
      }

      const selectedFile = { path: null, content: null };
      const history = [];

      // ---- Panels ----
      const treePanel = window.H.el('div', 'x-panel x-tree');
      treePanel.appendChild(window.H.el('div', 'pane-title', 'Explorer'));
      const treeDiv = window.H.el('div', 'x-tree');
      treeDiv.id = 'x-tree';
      treePanel.appendChild(treeDiv);

      const chatPanel = window.H.el('div', 'x-panel x-chat');
      chatPanel.appendChild(window.H.el('div', 'pane-title', 'Explain'));
      const chatLog = window.H.el('div', 'chat chat-log');
      chatPanel.appendChild(chatLog);
      const inputRow = window.H.el('div', 'chat-input');
      const textarea = window.H.el('textarea');
      textarea.placeholder = 'Ask about the code…';
      const sendBtn = window.H.el('button', 'btn', 'Send');
      inputRow.appendChild(textarea);
      inputRow.appendChild(sendBtn);
      chatPanel.appendChild(inputRow);
      const actionsRow = window.H.el('div', 'chat-actions');
      const explainBtn = window.H.el('button', 'btn secondary', 'Explain selected file');
      const guideBtn = window.H.el('button', 'btn secondary', 'Guide from floorplan');
      actionsRow.appendChild(explainBtn);
      actionsRow.appendChild(guideBtn);
      chatPanel.appendChild(actionsRow);

      const filePanel = window.H.el('div', 'x-panel x-file');
      const fileTitle = window.H.el('div', 'pane-title', 'No file selected');
      filePanel.appendChild(fileTitle);
      const fileBody = window.H.el('div', 'out');
      filePanel.appendChild(fileBody);

      const floorPanel = window.H.el('div', 'x-panel x-floorplan');
      floorPanel.appendChild(window.H.el('div', 'pane-title', 'Floorplan'));
      const floorBody = window.H.el('div', 'x-floorplan');
      floorPanel.appendChild(floorBody);

      containerEl.appendChild(treePanel);
      containerEl.appendChild(chatPanel);
      containerEl.appendChild(filePanel);
      containerEl.appendChild(floorPanel);

      // ---- Chat helpers ----
      function addMsg(role, content) {
        const msg = window.H.el('div', 'msg ' + role);
        const roleLabel = window.H.el('div', 'role', role === 'user' ? 'You' : 'Assistant');
        const bubble = window.H.el('div', 'bubble', content);
        msg.appendChild(roleLabel);
        msg.appendChild(bubble);
        chatLog.appendChild(msg);
        chatLog.scrollTop = chatLog.scrollHeight;
      }

      async function send(historySnapshot) {
        try {
          const reply = await window.H.sendChat(historySnapshot, 'explain', {});
          if (reply && reply.reply) {
            addMsg('assistant', reply.reply);
            history.push({ role: 'assistant', content: reply.reply });
          }
        } catch (e) {
          addMsg('assistant', 'Chat error: ' + (e && e.message ? e.message : e));
        }
      }

      // ---- Tree ----
      if (window.FileTree && window.FileTree.init) {
        try {
          window.FileTree.init(treeDiv, (path, content) => {
            selectedFile.path = path;
            selectedFile.content = content;
            fileTitle.textContent = path || 'No file selected';
            fileBody.textContent = content || '';
          });
        } catch (e) {
          fail(treePanel, 'File tree error: ' + (e && e.message ? e.message : e));
        }
      } else {
        fail(treePanel, 'FileTree not loaded.');
      }

      // ---- Chat input ----
      async function handleUserText(text) {
        if (!text) return;
        addMsg('user', text);
        history.push({ role: 'user', content: text });
        await send(history);
      }
      sendBtn.addEventListener('click', () => {
        handleUserText(textarea.value);
        textarea.value = '';
      });
      textarea.addEventListener('keydown', (ev) => {
        if (ev.key === 'Enter' && !ev.shiftKey) {
          ev.preventDefault();
          handleUserText(textarea.value);
          textarea.value = '';
        }
      });

      // ---- Explain selected file ----
      explainBtn.addEventListener('click', async () => {
        if (!selectedFile.path) {
          addMsg('assistant', 'Select a file in the tree first.');
          return;
        }
        const content = (selectedFile.content || '').slice(0, 4000);
        const text = 'Explain this file: ' + selectedFile.path + '\n\n' + content;
        addMsg('user', text);
        history.push({ role: 'user', content: text });
        await send(history);
      });

      // ---- Guide from floorplan ----
      guideBtn.addEventListener('click', async () => {
        let plan;
        try {
          const data = await window.H.fetchJSON('/api/floorplan');
          if (!data.exists) {
            addMsg('assistant', data.hint || 'No floorplan exists yet.');
            return;
          }
          plan = data.plan;
        } catch (e) {
          addMsg('assistant', 'Could not load floorplan: ' + (e && e.message ? e.message : e));
          return;
        }
        const text = 'Here is the floorplan blueprint: ' + JSON.stringify(plan) +
          '\nGuide me on what to build next and how.';
        addMsg('user', text);
        history.push({ role: 'user', content: text });
        await send(history);
      });

      // ---- Floorplan visual ----
      (async () => {
        if (!window.Visuals || !window.Visuals.renderFloorplan) {
          fail(floorPanel, 'Visuals.renderFloorplan not loaded.');
          return;
        }
        try {
          const data = await window.H.fetchJSON('/api/floorplan');
          if (data.exists && data.plan) {
            window.Visuals.renderFloorplan(floorBody, data.plan);
          } else {
            fail(floorPanel, data.hint || 'No floorplan exists yet.');
          }
        } catch (e) {
          fail(floorPanel, 'Could not load floorplan: ' + (e && e.message ? e.message : e));
        }
      })();
    }
  };
})();
