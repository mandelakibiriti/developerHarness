"use strict";

// Project file tree + file-content preview. Loaded after shell.js defines
// window.H = { el, fetchJSON, state, ... }. Exposes window.FileTree.init(el).
(function () {
  const H = window.H;

  // Top-level container, captured at init so the file preview can replace the
  // whole tree (not just a nested .tree-children div) and "back" re-renders
  // from the root.
  let rootEl = null;
  let onOpenCb = null;

  function joinPath(base, name) {
    return base ? base + "/" + name : name;
  }

  function errMsg(err) {
    return err && err.message ? err.message : String(err);
  }

  function renderHint(container, message) {
    container.innerHTML = "";
    container.appendChild(H.el("div", "hint", message));
  }

  function filesUrl(path, mode) {
    const qs = "path=" + encodeURIComponent(path);
    return "/api/files?" + qs + (mode ? "&mode=" + mode : "");
  }

  async function renderTree(container, path) {
    container.innerHTML = "";
    let data;
    try {
      data = await H.fetchJSON(filesUrl(path, ""));
    } catch (err) {
      renderHint(container, errMsg(err));
      return;
    }

    const entries = (data && data.entries) || [];
    if (!entries.length) {
      container.appendChild(H.el("div", "hint", "Empty directory"));
      return;
    }

    for (const entry of entries) {
      if (!entry) continue;
      const name = typeof entry.name === "string" ? entry.name : "";
      const isDir = entry.is_dir === true;
      const childPath = joinPath(path, name);

      const row = H.el("div", "tree-row");
      const tw = H.el("span", "tw", isDir ? "\u25B8" : ""); // ▸ for dirs
      const label = H.el("span", isDir ? "dir" : "file", name);
      row.appendChild(tw);
      row.appendChild(label);
      container.appendChild(row);

      if (isDir) {
        const children = H.el("div", "tree-children");
        children.style.display = "none";
        container.appendChild(children);
        let loaded = false;

        row.addEventListener("click", async () => {
          const expanded = children.style.display !== "none";
          if (expanded) {
            children.style.display = "none";
            tw.textContent = "\u25B8";
            return;
          }
          children.style.display = "";
          tw.textContent = "\u25BE"; // ▾
          if (!loaded) {
            loaded = true;
            await renderTree(children, childPath);
          }
        });
      } else {
        row.addEventListener("click", async () => {
          if (onOpenCb) {
            let data;
            try { data = await H.fetchJSON(filesUrl(childPath, "read")); }
            catch (err) { renderHint(rootEl, errMsg(err)); return; }
            onOpenCb(childPath, data && data.content != null ? String(data.content) : "");
          } else {
            showPreview(childPath);
          }
        });
      }
    }
  }

  async function showPreview(path) {
    let data;
    try {
      data = await H.fetchJSON(filesUrl(path, "read"));
    } catch (err) {
      renderHint(rootEl, errMsg(err));
      return;
    }

    const content =
      data && data.content != null ? String(data.content) : "";
    rootEl.innerHTML = "";

    const back = H.el("a", "btn secondary", "\u2190 back");
    back.href = "#";
    back.addEventListener("click", (e) => {
      e.preventDefault();
      renderTree(rootEl, "");
    });

    const meta = H.el("div", "meta-row muted", path);
    const pre = H.el("pre", "out", content);

    rootEl.appendChild(back);
    rootEl.appendChild(meta);
    rootEl.appendChild(pre);
  }

  window.FileTree = {
    init(containerEl, onOpen) {
      rootEl = containerEl;
      onOpenCb = typeof onOpen === "function" ? onOpen : null;
      renderTree(rootEl, "");
    },
  };
})();
