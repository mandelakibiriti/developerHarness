// API handlers for the native harness views: LLM settings + chat, architecture
// docs, floorplan blueprint, knowledge graph, plannotator review, and the
// skill-level toggle. Everything writes into the project (docs/, floorplan/,
// graphify-out/) rather than redirecting to another app.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devex/harness/internal/compiler"
	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/llm"
	"github.com/devex/harness/internal/track"
)

func (rt *Runtime) llmConfigPath() string { return filepath.Join(rt.home, "llm.json") }
func (rt *Runtime) docsDir() string       { return filepath.Join(rt.root, "docs") }
func (rt *Runtime) floorplanDir() string  { return filepath.Join(rt.root, "floorplan") }
func (rt *Runtime) graphFile() string     { return filepath.Join(rt.root, "graphify-out", "graph.json") }

func (rt *Runtime) readLLMFromDisk() *llm.Config {
	cfg := &llm.Config{}
	if b, err := os.ReadFile(rt.llmConfigPath()); err == nil {
		_ = json.Unmarshal(b, cfg)
	}
	return cfg
}

func (rt *Runtime) currentLLM() *llm.Config {
	rt.llmMu.Lock()
	defer rt.llmMu.Unlock()
	if rt.llmCfg == nil {
		rt.llmCfg = &llm.Config{}
	}
	return rt.llmCfg
}

// --- /api/llm ---

type llmStatus struct {
	Providers []llm.Provider `json:"providers"`
	Provider  string         `json:"provider"`
	Model     string         `json:"model"`
	BaseURL   string         `json:"base_url"`
	HasKey    bool           `json:"has_key"`
}

func (rt *Runtime) handleLLM(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := rt.currentLLM()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llmStatus{
			Providers: llm.Providers,
			Provider:  cfg.Provider,
			Model:     cfg.Model,
			BaseURL:   cfg.BaseURL,
			HasKey:    cfg.APIKey != "",
		})
	case http.MethodPut:
		var req struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			BaseURL  string `json:"base_url"`
			APIKey   string `json:"api_key"`
			// Optional USD per million tokens; enables cost events for this agent.
			USDPerMTokIn  float64 `json:"usd_per_mtok_in"`
			USDPerMTokOut float64 `json:"usd_per_mtok_out"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		p, ok := llm.Lookup(req.Provider)
		if !ok && req.BaseURL == "" {
			http.Error(w, "unknown provider and no base_url override", http.StatusBadRequest)
			return
		}
		base := req.BaseURL
		if base == "" {
			base = p.BaseURL
		}
		model := req.Model
		if model == "" && ok && len(p.Models) > 0 {
			model = p.Models[0]
		}
		cfg := &llm.Config{Provider: req.Provider, Model: model, BaseURL: base, APIKey: req.APIKey,
			USDPerMTokIn: req.USDPerMTokIn, USDPerMTokOut: req.USDPerMTokOut}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(rt.llmConfigPath(), b, 0o600); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rt.llmMu.Lock()
		rt.llmCfg = cfg
		rt.llmMu.Unlock()
		rt.log.Append(events.Event{Ev: "llm-configured", Data: map[string]any{"provider": cfg.Provider, "model": cfg.Model}})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llmStatus{Providers: llm.Providers, Provider: cfg.Provider, Model: cfg.Model, BaseURL: cfg.BaseURL, HasKey: cfg.APIKey != ""})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- /api/chat ---

type chatRequest struct {
	Messages []llm.Message `json:"messages"`
	Mode     string        `json:"mode"`
	Level    string        `json:"level"`
	Provider string        `json:"provider"`
	Model    string        `json:"model"`
	Domain   string        `json:"domain"` // lessons domain, e.g. "go"; "" = general
}

func (rt *Runtime) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	cfg := rt.currentLLM()
	if cfg.APIKey == "" {
		http.Error(w, "no API key configured — add one in Settings", http.StatusBadRequest)
		return
	}
	level := req.Level
	if level == "" {
		level = rt.cfg.Level
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	reply, err := rt.chat(ctx, "chat", req.Provider, req.Model,
		compiler.Request{Mode: req.Mode, Level: level, Domain: req.Domain}, req.Messages)
	if err != nil {
		http.Error(w, "chat failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	rt.log.Append(events.Event{Ev: "chat", Data: map[string]any{"mode": req.Mode, "level": level, "chars": len(reply)}})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"reply": reply})
}

// --- /api/docs ---

type docEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (rt *Runtime) handleDocs(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	switch {
	case r.Method == http.MethodGet && name == "":
		entries, _ := os.ReadDir(rt.docsDir())
		out := []docEntry{}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			if fi, err := e.Info(); err == nil {
				out = append(out, docEntry{Name: e.Name(), Size: fi.Size()})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"files": out, "dir": rt.docsDir()})
	case r.Method == http.MethodGet:
		b, err := os.ReadFile(filepath.Join(rt.docsDir(), name))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"name": name, "content": string(b)})
	case r.Method == http.MethodPut:
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(rt.docsDir(), 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(filepath.Join(rt.docsDir(), name), []byte(req.Content), 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rt.log.Append(events.Event{Ev: "doc-write", Data: map[string]any{"name": name}})
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- /api/docs/generate ---

func (rt *Runtime) handleDocsGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Goal     string `json:"goal"`
		Level    string `json:"level"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	cfg := rt.currentLLM()
	if cfg.APIKey == "" {
		http.Error(w, "no API key configured", http.StatusBadRequest)
		return
	}
	prompt := "Write the technical specification for this goal: " + req.Goal + ".\n" +
		"Respond with ONLY a JSON object (no markdown fences, no prose) of the form " +
		`{"files":[{"name":"architecture.md","content":"..."},{"name":"functional-requirements.md","content":"..."}]}. ` +
		"Split it into at least these files: architecture.md (components, boundaries, data flow, trust/security), " +
		"functional-requirements.md (user stories and acceptance criteria), and api-contract.md (endpoints, request/response, " +
		"versioning). Keep each file focused Markdown."
	reply, err := rt.chat(r.Context(), "docs-generate", req.Provider, req.Model,
		compiler.Request{Mode: "architect", Level: req.Level}, []llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		http.Error(w, "generate failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	raw, err := extractJSON(reply)
	if err != nil {
		http.Error(w, "could not parse model output as JSON", http.StatusBadGateway)
		return
	}
	var parsed struct {
		Files []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Files) == 0 {
		http.Error(w, "model returned no files", http.StatusBadGateway)
		return
	}
	if err := os.MkdirAll(rt.docsDir(), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var written []string
	for _, f := range parsed.Files {
		name := filepath.Base(f.Name)
		if !strings.HasSuffix(name, ".md") {
			name += ".md"
		}
		if err := os.WriteFile(filepath.Join(rt.docsDir(), name), []byte(f.Content), 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		written = append(written, name)
	}
	rt.log.Append(events.Event{Ev: "spec-generated", Data: map[string]any{"goal": req.Goal, "files": written}})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"files": written, "dir": rt.docsDir()})
}

func extractJSON(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	if !strings.Contains(s, "{") {
		return nil, fmt.Errorf("no JSON object in output")
	}
	return []byte(s), nil
}

// --- /api/level ---

func (rt *Runtime) handleLevel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	tr, err := track.Load(filepath.Join(rt.root, "tracks", req.Level+".json"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rt.SetTrack(tr)
	rt.cfg.Level = req.Level
	rt.cfg.Profile = tr.Profile
	if rt.configPath != "" {
		if b, err := os.ReadFile(rt.configPath); err == nil {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				m["level"] = req.Level
				m["profile"] = tr.Profile
				if out, err := json.MarshalIndent(m, "", "  "); err == nil {
					_ = os.WriteFile(rt.configPath, out, 0o644)
				}
			}
		}
	}
	rt.log.Append(events.Event{Ev: "level", Data: map[string]any{"level": req.Level, "profile": tr.Profile, "agent_mode": tr.AgentMode}})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"level": req.Level, "profile": tr.Profile, "agent_mode": tr.AgentMode})
}

// --- /api/floorplan ---

func (rt *Runtime) handleFloorplan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := os.ReadFile(filepath.Join(rt.floorplanDir(), "plan.json"))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"exists": false, "hint": "Generate a blueprint from the spec, or write floorplan/plan.json."})
		return
	}
	var v any
	_ = json.Unmarshal(b, &v)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"exists": true, "plan": v})
}

func (rt *Runtime) handleFloorplanGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Goal     string `json:"goal"`
		Level    string `json:"level"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	cfg := rt.currentLLM()
	if cfg.APIKey == "" {
		http.Error(w, "no API key configured", http.StatusBadRequest)
		return
	}
	prompt := "Produce a Floorplan blueprint (plan.json) for: " + req.Goal + ".\n" +
		"Respond with ONLY a JSON object: " +
		`{"goal":"...","components":[{"id":"...","name":"...","description":"...","depends_on":["..."]}],"endpoints":[{"method":"GET","path":"/v1/...","purpose":"..."}],"screens":[{"id":"...","name":"...","calls":["..."]}],"build_order":["component id first to build first..."]}. ` +
		"Base it on the docs/ specification if present. Prefer a modular monolith; production-grade and secure-by-design."
	reply, err := rt.chat(r.Context(), "floorplan-generate", req.Provider, req.Model,
		compiler.Request{Mode: "architect", Level: req.Level}, []llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		http.Error(w, "generate failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	raw, err := extractJSON(reply)
	if err != nil {
		http.Error(w, "could not parse model output as JSON", http.StatusBadGateway)
		return
	}
	if err := os.MkdirAll(rt.floorplanDir(), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(rt.floorplanDir(), "plan.json"), raw, 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rt.log.Append(events.Event{Ev: "floorplan-generated", Data: map[string]any{"goal": req.Goal}})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"written": filepath.Join(rt.floorplanDir(), "plan.json")})
}

// --- /api/graph ---

func (rt *Runtime) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := os.ReadFile(rt.graphFile())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"exists": false, "hint": "No knowledge graph yet. Build one by running /graphify in Claude Code (or point graphify-out/graph.json at this project), then traverse it here."})
		return
	}
	var v any
	_ = json.Unmarshal(b, &v)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"exists": true, "graph": v})
}

func (rt *Runtime) handleGraphQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(rt.graphFile()); err != nil {
		http.Error(w, "no graph built yet", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "graphify", "query", req.Question, "--graph", rt.graphFile()).CombinedOutput()
	if err != nil {
		http.Error(w, "query failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"answer": string(out)})
}

// --- /api/review ---

func (rt *Runtime) handleReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := os.Stat(rt.docsDir()); err != nil {
		http.Error(w, "no docs/ to review yet", http.StatusBadRequest)
		return
	}
	resultFile := filepath.Join(rt.home, "review-result.json")
	cmd := exec.Command("plannotator", "annotate", rt.docsDir()+"/", "--result-file", resultFile, "--json")
	cmd.Stdout = os.Stderr // annotate prints a URL/session; keep it visible in logs
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		http.Error(w, "failed to start plannotator: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rt.log.Append(events.Event{Ev: "review-started", Data: map[string]any{"result_file": resultFile, "target": rt.docsDir()}})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started", "result_file": resultFile, "note": "Complete the review in Plannotator; the result lands in the file above."})
}
