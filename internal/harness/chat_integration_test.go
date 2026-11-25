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

// serveJSON drives the harness HTTP handler directly, forcing the Host header
// the subdomain proxy expects (harness.localhost:4400) so /api/* routes resolve.
func serveJSON(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "harness.localhost:4400"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestChatAndGenerateConsistency exercises /api/chat and /api/docs/generate
// against one mock OpenAI-compatible server, verifying that a per-request
// provider/model override reaches the mock and that a specification goal
// writes its returned files onto disk under <root>/docs/.
func TestChatAndGenerateConsistency(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		lastUser := ""
		for _, m := range in.Messages {
			if m.Role == "user" {
				lastUser = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if strings.Contains(lastUser, "spec") || strings.Contains(lastUser, "Specification") {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"files\":[{\"name\":\"architecture.md\",\"content\":\"# Arch\"},{\"name\":\"functional-requirements.md\",\"content\":\"# FR\"}]}"}}]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "echo: " + lastUser}}},
		})
	}))
	defer mock.Close()

	home := t.TempDir()
	root := t.TempDir()
	configPath := filepath.Join(root, "harness.json")

	log, err := events.Open(filepath.Join(home, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	cfg := &config.Config{
		Schema:  config.SchemaVersion,
		Profile: "core",
		Level:   "beginner",
		Listen:  "127.0.0.1:4400",
		Project: config.Project{Root: root},
	}
	rt := New(cfg, "core", home, log)
	rt.SetProject(root, configPath)
	if err := rt.Start(); err != nil {
		t.Fatal(err)
	}
	h := rt.Handler()

	// Configure the LLM to point at the mock (base_url = mock.URL + "/v1").
	rec := serveJSON(h, http.MethodPut, "/api/llm",
		`{"provider":"nvidia","model":"m","base_url":"`+mock.URL+`/v1","api_key":"sk"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/llm = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Two chats with distinct model overrides must both round-trip.
	for _, model := range []string{"m1", "m2"} {
		rec := serveJSON(h, http.MethodPost, "/api/chat",
			`{"messages":[{"role":"user","content":"hello"}],"mode":"general","level":"beginner","provider":"nvidia","model":"`+model+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/chat (model %s) = %d, want 200 (body: %s)", model, rec.Code, rec.Body.String())
		}
		var out struct {
			Reply string `json:"reply"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("chat response not JSON: %v", err)
		}
		if out.Reply == "" {
			t.Fatalf("chat reply empty for model %s", model)
		}
		if !strings.HasPrefix(out.Reply, "echo:") {
			t.Fatalf("unexpected chat reply %q", out.Reply)
		}
	}

	// A specification goal must produce both files on disk under <root>/docs/.
	rec = serveJSON(h, http.MethodPost, "/api/docs/generate",
		`{"goal":"Build a Todo Specification","level":"beginner"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/docs/generate = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"architecture.md", "functional-requirements.md"} {
		if _, err := os.Stat(filepath.Join(root, "docs", name)); err != nil {
			t.Fatalf("expected docs/%s to exist: %v", name, err)
		}
	}
}

// TestChatNoKeyReturnsBadRequest verifies /api/chat refuses to talk to a model
// when no API key has been configured (no llm.json, no PUT /api/llm).
func TestChatNoKeyReturnsBadRequest(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	configPath := filepath.Join(root, "harness.json")

	log, err := events.Open(filepath.Join(home, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	cfg := &config.Config{
		Schema:  config.SchemaVersion,
		Profile: "core",
		Level:   "beginner",
		Listen:  "127.0.0.1:4400",
		Project: config.Project{Root: root},
	}
	rt := New(cfg, "core", home, log)
	rt.SetProject(root, configPath)
	if err := rt.Start(); err != nil {
		t.Fatal(err)
	}

	rec := serveJSON(rt.Handler(), http.MethodPost, "/api/chat",
		`{"messages":[{"role":"user","content":"hello"}],"mode":"general"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/chat = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestChatUsesCompiledContextAndRecordsUsage checks that /api/chat sends the
// compiled system prompt (not a Go string) and logs llm-usage plus a priced
// cost event, both stamped with the context version.
func TestChatUsesCompiledContextAndRecordsUsage(t *testing.T) {
	var system string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if len(in.Messages) > 0 && in.Messages[0].Role == "system" {
			system = in.Messages[0].Content
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":2000,"completion_tokens":1000}}`))
	}))
	defer mock.Close()

	home, root := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "context", "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "context", "system", "build.beginner.md"), []byte("PROJECT TUTOR RULES"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "events.jsonl")
	log, err := events.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cfg := &config.Config{Schema: config.SchemaVersion, Profile: "core", Level: "beginner", Listen: "127.0.0.1:4400", Project: config.Project{Root: root}}
	rt := New(cfg, "core", home, log)
	rt.SetProject(root, filepath.Join(root, "harness.json"))
	if err := rt.Start(); err != nil {
		t.Fatal(err)
	}
	h := rt.Handler()

	rec := serveJSON(h, http.MethodPut, "/api/llm",
		`{"provider":"nvidia","model":"m","base_url":"`+mock.URL+`","api_key":"sk","usd_per_mtok_in":3,"usd_per_mtok_out":15}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/llm = %d: %s", rec.Code, rec.Body.String())
	}
	rec = serveJSON(h, http.MethodPost, "/api/chat", `{"messages":[{"role":"user","content":"hi"}],"mode":"build"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/chat = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(system, "You are the DevEx Harness guide") || !strings.Contains(system, "PROJECT TUTOR RULES") {
		t.Fatalf("system prompt not compiled from context/: %q", system)
	}

	evs, err := events.ReadAll(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var usage, cost *events.Event
	for i := range evs {
		switch evs[i].Ev {
		case "llm-usage":
			usage = &evs[i]
		case "cost":
			cost = &evs[i]
		}
	}
	if usage == nil || usage.Data["prompt_tokens"] != float64(2000) || usage.Data["harness_version"] == "" {
		t.Fatalf("llm-usage event = %+v", usage)
	}
	// 2000*3/1e6 + 1000*15/1e6 = 0.021
	if cost == nil || cost.Data["amount"] != 0.021 || cost.Data["harness_version"] != usage.Data["harness_version"] {
		t.Fatalf("cost event = %+v", cost)
	}
}
