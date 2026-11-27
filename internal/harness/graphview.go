package harness

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
)

// --- /graph/view: graphify's own graph.html, embedded in the Graph tab ---
//
// The file is generated from project content, so it is served sandboxed (opaque
// origin): its scripts cannot call harness APIs. The tab talks to it only by
// postMessage through the bridge appended here.

const graphCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' https://unpkg.com; " +
	"style-src 'unsafe-inline'; img-src data:"

const graphBridge = `<script>
(function () {
  var send = function (m) { m.graph = 1; parent.postMessage(m, '*'); };
  var side = document.getElementById('sidebar');
  var themes = document.getElementById('theme-wrap');
  if (side) side.style.display = 'none';
  if (themes) themes.style.display = 'none'; // the harness theme drives it
  var info = window.showInfo;
  window.showInfo = function (id) { info(id); send({ type: 'select', id: id }); };
  addEventListener('message', function (e) {
    if (e.source !== parent || !e.data) return;
    var m = e.data;
    try {
      if (m.type === 'focus') focusNode(m.id);
      if (m.type === 'theme') applyTheme(m.theme);
      if (m.type === 'sidebar' && side) side.style.display = m.open ? '' : 'none';
    } catch (err) { send({ type: 'missing', id: m.id }); }
  });
  send({ type: 'ready' });
})();
</script>
`

func (rt *Runtime) handleGraphView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := os.ReadFile(filepath.Join(rt.root, "graphify-out", "graph.html"))
	if err != nil {
		http.Error(w, "no graphify-out/graph.html yet: run /graphify in Claude Code", http.StatusNotFound)
		return
	}
	if i := bytes.LastIndex(b, []byte("</body>")); i >= 0 {
		b = append(b[:i:i], append([]byte(graphBridge), b[i:]...)...)
	} else {
		b = append(b, graphBridge...)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", graphCSP)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}
