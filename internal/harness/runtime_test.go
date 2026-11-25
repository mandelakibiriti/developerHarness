package harness

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/events"
)

func boolPtr(b bool) *bool { return &b }

func TestRuntimeUnmanagedDiscovery(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer up.Close()

	cfg := &config.Config{
		Schema:  config.SchemaVersion,
		Profile: "core",
		Level:   "beginner",
		Listen:  "127.0.0.1:4400",
		Project: config.Project{Root: ".", AppPorts: []int{8080}},
		Tools: []config.Tool{
			{ID: "map", Tab: "Map", Profile: "core", Managed: boolPtr(false), Health: up.URL},
		},
	}
	log, err := events.Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	rt := New(cfg, "core", t.TempDir(), log)
	if err := rt.Start(); err != nil {
		t.Fatal(err)
	}
	rt.poll()

	st := rt.Status()
	if len(st) != 1 {
		t.Fatalf("want 1 tool, got %d", len(st))
	}
	if st[0].Upstream != up.URL {
		t.Fatalf("upstream = %q, want %q", st[0].Upstream, up.URL)
	}
	if !st[0].Healthy {
		t.Fatal("expected healthy after poll")
	}
	if st[0].Status != "healthy" {
		t.Fatalf("status = %q", st[0].Status)
	}
}

func TestScopeGuardRefusesUntilConfirmed(t *testing.T) {
	cfg := &config.Config{
		Schema:  config.SchemaVersion,
		Profile: "security",
		Level:   "advanced",
		Listen:  "127.0.0.1:4400",
		Project: config.Project{Root: "."},
		Tools: []config.Tool{
			{ID: "secure", Tab: "Secure", Profile: "security", Command: []string{"sleep", "5"},
				Scope: &config.Scope{Allow: []string{"127.0.0.1"}, Ports: []int{8080}, Deny: []string{"*"}}},
		},
	}
	log, err := events.Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	rt := New(cfg, "security", t.TempDir(), log)
	if err := rt.Start(); err != nil {
		t.Fatal(err)
	}
	st := rt.Status()
	if len(st) != 1 {
		t.Fatalf("want 1 tool, got %d", len(st))
	}
	if st[0].Status != "error" || !strings.Contains(st[0].Error, "scope mapping not confirmed") {
		t.Fatalf("expected scope-refusal error, got %+v", st[0])
	}
}

func TestScopeGuardRefusesNonLoopback(t *testing.T) {
	cfg := &config.Config{
		Schema:  config.SchemaVersion,
		Profile: "security",
		Level:   "advanced",
		Listen:  "127.0.0.1:4400",
		Project: config.Project{Root: "."},
		Tools: []config.Tool{
			{ID: "secure", Tab: "Secure", Profile: "security", Command: []string{"sleep", "5"},
				Scope: &config.Scope{Allow: []string{"example.com"}, Ports: []int{8080}, Deny: []string{"*"}}},
		},
	}
	log, err := events.Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	rt := New(cfg, "security", t.TempDir(), log)
	_ = rt.Start()
	st := rt.Status()
	if len(st) != 1 || st[0].Status != "error" || !strings.Contains(st[0].Error, "loopback-only") {
		t.Fatalf("expected loopback-only refusal, got %+v", st)
	}
}

func TestHandlerServesStatusAndShell(t *testing.T) {
	cfg := &config.Config{
		Schema:  config.SchemaVersion,
		Profile: "core",
		Level:   "beginner",
		Listen:  "127.0.0.1:4400",
		Project: config.Project{Root: "."},
	}
	log, err := events.Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	rt := New(cfg, "core", t.TempDir(), log)
	_ = rt.Start()

	h := rt.Handler()

	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Host = "harness.localhost:4400"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status code %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}

	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Host = "harness.localhost:4400"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("shell code %d", rec2.Code)
	}
}
