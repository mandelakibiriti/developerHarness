package harness

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphViewIsSandboxedAndBridged(t *testing.T) {
	root := t.TempDir()
	rt := &Runtime{root: root}
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		rt.handleGraphView(rec, httptest.NewRequest(http.MethodGet, "/graph/view", nil))
		return rec
	}
	if get().Code != http.StatusNotFound {
		t.Fatal("want 404 without graph.html")
	}
	os.MkdirAll(filepath.Join(root, "graphify-out"), 0o755)
	os.WriteFile(filepath.Join(root, "graphify-out", "graph.html"), []byte("<body><div id=graph></div></body></html>"), 0o644)
	rec := get()
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox allow-scripts;") || strings.Contains(csp, "allow-same-origin") {
		t.Fatalf("csp = %q", csp)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "postMessage") || !strings.HasSuffix(body, "</body></html>") {
		t.Fatalf("bridge not injected before </body>:\n%s", body)
	}
}
