// Additional API handlers: LLM test + model listing, per-request model
// selection, a project file tree, native-terminal launch, LearnHarness access,
// and token/cost visibility (TokenTelemetry + Agent Trail + the in-harness
// budget).
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

	harnessctx "github.com/devex/harness/context"
	"github.com/devex/harness/internal/compiler"
	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/llm"
)

// clientFor builds a client for an optional per-request provider/model override,
// reusing the saved API key. The override picks the base URL from the provider
// defaults; switching a provider's key is done in Settings.
// effectiveLLM applies a per-request provider/model override to the saved
// config. Saved prices are kept only when the model is unchanged, so an
// override is never priced at another model's rate.
func (rt *Runtime) effectiveLLM(provider, model string) llm.Config {
	cfg := rt.currentLLM()
	eff := *cfg
	if provider != "" && provider != cfg.Provider {
		if p, ok := llm.Lookup(provider); ok {
			eff.Provider = provider
			eff.BaseURL = p.BaseURL
		}
	}
	if model != "" {
		eff.Model = model
	}
	if eff.Provider != cfg.Provider || eff.Model != cfg.Model {
		eff.USDPerMTokIn, eff.USDPerMTokOut = 0, 0
	}
	return eff
}

// compile builds the context bundle for one model call. Level defaults to the
// harness level.
func (rt *Runtime) compile(req compiler.Request) (compiler.Bundle, error) {
	if req.Level == "" {
		req.Level = rt.cfg.Level
	}
	c := &compiler.Compiler{Root: rt.root, Cfg: rt.cfg, Defaults: harnessctx.FS}
	return c.Compile(req)
}

// chat is the one path every harness model call takes: compile the context,
// call the model, then record token usage (and cost, when prices are set) on
// the shared log stamped with the context version, so Spend and the judge see
// this agent too.
func (rt *Runtime) chat(ctx context.Context, purpose, provider, model string, creq compiler.Request, msgs []llm.Message) (string, error) {
	b, err := rt.compile(creq)
	if err != nil {
		return "", fmt.Errorf("compile context: %w", err)
	}
	eff := rt.effectiveLLM(provider, model)
	all := append([]llm.Message{{Role: "system", Content: b.System()}}, msgs...)
	reply, usage, err := llm.New(eff).Chat(ctx, all)
	if err != nil {
		return "", err
	}
	rt.log.Append(events.Event{Ev: "llm-usage", Tool: "llm", Data: map[string]any{
		"purpose": purpose, "provider": eff.Provider, "model": eff.Model,
		"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.CompletionTokens,
		"harness_version": b.Version,
	}})
	if usd, ok := eff.CostUSD(usage); ok {
		rt.log.Append(events.Event{Ev: "cost", Tool: "llm", Data: map[string]any{
			"amount": usd, "currency": "USD", "feature": purpose, "harness_version": b.Version,
		}})
	}
	return reply, nil
}

// --- /api/llm/test ---

func (rt *Runtime) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	key := req.APIKey
	if key == "" {
		key = rt.currentLLM().APIKey
	}
	if key == "" {
		http.Error(w, "no API key", http.StatusBadRequest)
		return
	}
	base := req.BaseURL
	if base == "" {
		if p, ok := llm.Lookup(req.Provider); ok {
			base = p.BaseURL
		}
	}
	if base == "" {
		http.Error(w, "unknown provider and no base_url", http.StatusBadRequest)
		return
	}
	model := req.Model
	if model == "" {
		if p, ok := llm.Lookup(req.Provider); ok && len(p.Models) > 0 {
			model = p.Models[0]
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	client := llm.New(llm.Config{Provider: req.Provider, Model: model, BaseURL: base, APIKey: key})
	models, err := client.Test(ctx)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "models": models})
}

// --- /api/llm/models (live model list for a provider) ---

func (rt *Runtime) handleLLMModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider := r.URL.Query().Get("provider")
	cfg := rt.currentLLM()
	eff := *cfg
	if provider != "" && provider != cfg.Provider {
		if p, ok := llm.Lookup(provider); ok {
			eff.Provider = provider
			eff.BaseURL = p.BaseURL
		}
	}
	if eff.APIKey == "" {
		http.Error(w, "no API key configured", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	models, err := llm.New(eff).Models(ctx)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"models": nil, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"models": models})
}

// --- /api/files (project file tree) ---

func (rt *Runtime) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	relPath := r.URL.Query().Get("path")
	mode := r.URL.Query().Get("mode")
	abs := rt.root
	if relPath != "" {
		abs = filepath.Join(rt.root, relPath)
		if rel, err := filepath.Rel(rt.root, abs); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			http.Error(w, "path outside project", http.StatusBadRequest)
			return
		}
	}
	if mode == "read" {
		fi, err := os.Stat(abs)
		if err != nil || fi.IsDir() {
			http.Error(w, "not a file", http.StatusNotFound)
			return
		}
		if fi.Size() > 512*1024 {
			http.Error(w, "file too large", http.StatusBadRequest)
			return
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"path": relPath, "content": string(b)})
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	type entry struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
	}
	out := make([]entry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // hide dotfiles/.harness/.git
		}
		if e.IsDir() {
			out = append(out, entry{Name: name, IsDir: true})
		} else if fi, err := e.Info(); err == nil {
			out = append(out, entry{Name: name, Size: fi.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"path": relPath, "root": rt.root, "entries": out})
}

// --- /api/terminal (open the OS terminal at the project root) ---

func (rt *Runtime) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cmd := exec.Command("open", "-a", "Terminal", rt.root)
	if err := cmd.Run(); err != nil {
		http.Error(w, "failed to open terminal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rt.log.Append(events.Event{Ev: "terminal", Data: map[string]any{"dir": rt.root}})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "opened", "dir": rt.root})
}

// --- /api/learn (LearnHarness desktop app) ---

const learnAppPath = "/Applications/LearnHarness.app"

func (rt *Runtime) handleLearn(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_, err := os.Stat(learnAppPath)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"installed": err == nil, "path": learnAppPath})
	case http.MethodPost:
		if _, err := os.Stat(learnAppPath); err != nil {
			http.Error(w, "LearnHarness not found at "+learnAppPath, http.StatusNotFound)
			return
		}
		if err := exec.Command("open", learnAppPath).Run(); err != nil {
			http.Error(w, "failed to launch: "+err.Error(), http.StatusInternalServerError)
			return
		}
		rt.log.Append(events.Event{Ev: "learn-launch"})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "launched"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- /api/tokens (token + cost visibility) ---

func (rt *Runtime) handleTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	spend := probe("http://127.0.0.1:13000/")
	trail := probe("http://127.0.0.1:3030/api/v1/sessions/search")
	var budgetStatus any
	if rt.budget != nil {
		budgetStatus = rt.budget.Status()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"spend":  map[string]any{"available": spend.available, "url": "http://127.0.0.1:13000/", "status": spend.status},
		"trail":  map[string]any{"available": trail.available, "url": "http://127.0.0.1:3030/", "status": trail.status},
		"budget": budgetStatus,
	})
}

type probeResult struct {
	available bool
	status    int
}

func probe(url string) probeResult {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return probeResult{}
	}
	resp.Body.Close()
	return probeResult{available: resp.StatusCode < 500, status: resp.StatusCode}
}
