package harness

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/llm"
)

// newTestRuntime starts a runtime whose catalog is two fake providers on mock:
// "a" accepts key ka, "b" rejects everything.
func newTestRuntime(t *testing.T, home string) (http.Handler, *httptest.Server) {
	t.Helper()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch {
		case strings.HasPrefix(r.URL.Path, "/a/") && auth == "Bearer ka":
			if strings.HasSuffix(r.URL.Path, "/models") {
				w.Write([]byte(`{"data":[{"id":"a1"},{"id":"a2"}]}`))
				return
			}
			w.Write([]byte(`{"choices":[{"message":{"content":"from a"}}]}`))
		default:
			http.Error(w, `{"error":"bad key"}`, http.StatusUnauthorized)
		}
	}))
	t.Cleanup(mock.Close)
	orig := llm.Providers
	llm.Providers = []llm.Provider{
		{ID: "a", Label: "A", BaseURL: mock.URL + "/a", Models: []string{"a1"}},
		{ID: "b", Label: "B", BaseURL: mock.URL + "/b", Models: []string{"b1"}},
	}
	t.Cleanup(func() { llm.Providers = orig })

	root := t.TempDir()
	log, err := events.Open(filepath.Join(home, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	cfg := &config.Config{Schema: config.SchemaVersion, Profile: "core", Level: "beginner",
		Listen: "127.0.0.1:4400", Project: config.Project{Root: root}}
	rt := New(cfg, "core", home, log)
	rt.SetProject(root, filepath.Join(root, "harness.json"))
	if err := rt.Start(); err != nil {
		t.Fatal(err)
	}
	return rt.Handler(), mock
}

func TestPerProviderKeysAndAvailable(t *testing.T) {
	home := t.TempDir()
	h, _ := newTestRuntime(t, home)

	for _, body := range []string{
		`{"provider":"a","model":"a1","api_key":"ka"}`,
		`{"provider":"b","model":"b1","api_key":"kb"}`,
		`{"provider":"b","model":"b1"}`, // re-save without a key must keep kb
	} {
		if rec := serveJSON(h, http.MethodPut, "/api/llm", body); rec.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", body, rec.Code, rec.Body.String())
		}
	}

	var st llmStatus
	json.Unmarshal(serveJSON(h, http.MethodGet, "/api/llm", "").Body.Bytes(), &st)
	if strings.Join(st.Configured, ",") != "a,b" || !st.HasKey {
		t.Fatalf("configured = %v has_key=%v, want a,b true", st.Configured, st.HasKey)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "llm.json")); !strings.Contains(string(b), `"b": "kb"`) {
		t.Fatalf("llm.json lost key b:\n%s", b)
	}

	var av struct{ Providers []providerAvail }
	json.Unmarshal(serveJSON(h, http.MethodGet, "/api/llm/available", "").Body.Bytes(), &av)
	if len(av.Providers) != 2 || !av.Providers[0].OK || strings.Join(av.Providers[0].Models, ",") != "a1,a2" || av.Providers[1].OK {
		t.Fatalf("available = %+v, want a ok [a1 a2], b failing", av.Providers)
	}

	// Active provider is b, but chatting with a must use a's key.
	rec := serveJSON(h, http.MethodPost, "/api/chat", `{"messages":[{"role":"user","content":"hi"}],"mode":"general","provider":"a","model":"a1"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from a") {
		t.Fatalf("chat via a = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLegacySingleKeyFileMigrates(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "llm.json"), []byte(`{"provider":"a","model":"a1","api_key":"ka"}`), 0o600)
	h, _ := newTestRuntime(t, home)
	var st llmStatus
	json.Unmarshal(serveJSON(h, http.MethodGet, "/api/llm", "").Body.Bytes(), &st)
	if strings.Join(st.Configured, ",") != "a" {
		t.Fatalf("configured = %v, want [a]", st.Configured)
	}
}

func TestResaveKeepsBaseURLOverride(t *testing.T) {
	h, mock := newTestRuntime(t, t.TempDir())
	override := mock.URL + "/a"
	serveJSON(h, http.MethodPut, "/api/llm", `{"provider":"b","model":"b1","base_url":"`+override+`","api_key":"ka"}`)
	rec := serveJSON(h, http.MethodPut, "/api/llm", `{"provider":"b","model":"b1"}`)
	var st llmStatus
	json.Unmarshal(rec.Body.Bytes(), &st)
	if st.BaseURL != override {
		t.Fatalf("base_url = %q after re-save, want %q", st.BaseURL, override)
	}
}

func TestDocsRejectsPathTraversal(t *testing.T) {
	h, _ := newTestRuntime(t, t.TempDir())
	for _, name := range []string{"../harness.json", "..%2Fllm.json", "notes.txt", ".hidden.md"} {
		if rec := serveJSON(h, http.MethodGet, "/api/docs?name="+name, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("GET name=%s = %d, want 400", name, rec.Code)
		}
		if rec := serveJSON(h, http.MethodPut, "/api/docs?name="+name, `{"content":"x"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT name=%s = %d, want 400", name, rec.Code)
		}
	}
	if rec := serveJSON(h, http.MethodPut, "/api/docs?name=architecture.md", `{"content":"# A"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT architecture.md = %d", rec.Code)
	}
}
