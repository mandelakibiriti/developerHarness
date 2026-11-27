package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/llm"
)

// llmFile is the on-disk shape of .harness/llm.json: the active provider/model
// (llm.Config, unchanged for older readers) plus one API key per provider.
type llmFile struct {
	llm.Config
	Keys map[string]string `json:"keys,omitempty"`
}

// availEntry caches one provider's key check so page loads don't re-test keys.
type availEntry struct {
	key string // the key that was tested; a changed key invalidates the entry
	res providerAvail
}

type providerAvail struct {
	Provider string   `json:"provider"`
	Label    string   `json:"label"`
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
	Models   []string `json:"models"`
}

// keyFor returns the saved key for a provider ("" if none).
func (rt *Runtime) keyFor(provider string) string {
	rt.llmMu.Lock()
	defer rt.llmMu.Unlock()
	if k := rt.llmKeys[provider]; k != "" {
		return k
	}
	if rt.llmCfg != nil && rt.llmCfg.Provider == provider {
		return rt.llmCfg.APIKey
	}
	return ""
}

// configuredProviders lists provider ids that have a saved key, sorted.
func (rt *Runtime) configuredProviders() []string {
	rt.llmMu.Lock()
	defer rt.llmMu.Unlock()
	ids := []string{}
	for id, k := range rt.llmKeys {
		if k != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// baseFor uses the saved base_url override for the active provider, else the catalog URL.
func (rt *Runtime) baseFor(p llm.Provider) string {
	cfg := rt.currentLLM()
	if cfg.Provider == p.ID && cfg.BaseURL != "" {
		return cfg.BaseURL
	}
	return p.BaseURL
}

func (rt *Runtime) logKeyCheck(provider string, models int, d time.Duration, err error) {
	data := map[string]any{"provider": provider, "ok": err == nil, "models": models, "duration_ms": d.Milliseconds()}
	if err != nil {
		data["error"] = err.Error()
	}
	rt.log.Append(events.Event{Ev: "llm-key-check", Tool: "llm", Data: data})
}

// --- /api/llm/available: every provider with a saved key, and its working models ---

func (rt *Runtime) handleLLMAvailable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	out := make([]providerAvail, 0, len(llm.Providers))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, p := range llm.Providers {
		key := rt.keyFor(p.ID)
		if key == "" {
			continue
		}
		rt.llmMu.Lock()
		e, hit := rt.llmAvail[p.ID]
		rt.llmMu.Unlock()
		if hit && e.key == key && !refresh {
			mu.Lock()
			out = append(out, e.res)
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(p llm.Provider, key string) {
			defer wg.Done()
			res := rt.checkProvider(r.Context(), p, key)
			mu.Lock()
			out = append(out, res)
			mu.Unlock()
			rt.llmMu.Lock()
			rt.llmAvail[p.ID] = availEntry{key: key, res: res}
			rt.llmMu.Unlock()
		}(p, key)
	}
	wg.Wait()
	order := map[string]int{}
	for i, p := range llm.Providers {
		order[p.ID] = i
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i].Provider] < order[out[j].Provider] })
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"providers": out})
}

func (rt *Runtime) checkProvider(ctx context.Context, p llm.Provider, key string) providerAvail {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res := providerAvail{Provider: p.ID, Label: p.Label, Models: []string{}}
	model := ""
	if len(p.Models) > 0 {
		model = p.Models[0]
	}
	start := time.Now()
	models, err := llm.New(llm.Config{Provider: p.ID, Model: model, BaseURL: rt.baseFor(p), APIKey: key}).Test(ctx)
	rt.logKeyCheck(p.ID, len(models), time.Since(start), err)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	if len(models) == 0 {
		models = p.Models // key works but the provider has no /models list
	}
	res.Models = append(res.Models, models...)
	return res
}
