package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	harnessctx "github.com/devex/harness/context"
	"github.com/devex/harness/internal/budget"
	"github.com/devex/harness/internal/compiler"
	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/discovery"
	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/graph"
	"github.com/devex/harness/internal/llm"
	"github.com/devex/harness/internal/practice"
	"github.com/devex/harness/internal/scope"
)

// Env is what the harness connectors read from. They are read-only on purpose:
// nothing here can append to the event log, so model-written code can never
// forge the gate, cost or finding events that promotion and the judge trust.
type Env struct {
	Root string
	Cfg  *config.Config
}

const (
	defaultRecent = 20
	maxRecent     = 200
	httpTimeout   = 5 * time.Second
	defaultHits   = 8
	maxHits       = 30
)

var problemID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// HarnessConnectors returns the connectors over the harness and its tools.
func HarnessConnectors(env Env) []Connector {
	return []Connector{
		{
			Namespace: "events", Name: "recent",
			Description: "Most recent harness events (gate, cost, finding, llm-usage, PostToolUse, ...), oldest first.",
			Params: []Param{
				{Name: "n", Type: "number", Optional: true, Doc: fmt.Sprintf("default %d, max %d", defaultRecent, maxRecent)},
				{Name: "ev", Type: "string", Optional: true, Doc: "only events of this type, e.g. \"gate\""},
			},
			Returns: "{ t: string; ev: string; session?: string; tool?: string; sha?: string; data?: Record<string, unknown> }[]",
			Fn:      env.recentEvents,
		},
		{
			Namespace: "budget", Name: "status",
			Description: "Spend so far against the per-feature budget, from the cost events in the log.",
			Returns:     "{ spent: number; per_feature_usd: number; ratio: number; over: boolean; alert_at: number[] }",
			Fn:          env.budgetStatus,
		},
		{
			Namespace: "floorplan", Name: "plan",
			Description: "The Floorplan blueprint (floorplan/plan.json): components, endpoints, screens, build order.",
			Returns:     "Record<string, unknown>",
			Fn: func(context.Context, map[string]any) (any, error) {
				return readJSON(filepath.Join(env.Root, "floorplan", "plan.json"))
			},
		},
		{
			Namespace: "review", Name: "sessions",
			Description: "URLs of live Plannotator review sessions.",
			Returns:     "string[]",
			Fn:          env.reviewSessions,
		},
		{
			Namespace: "trail", Name: "search",
			Description: "Search Agent Trail's indexed Claude Code sessions (tool calls, subagents, injected context).",
			Params:      []Param{{Name: "q", Type: "string", Doc: "search text"}},
			Returns:     "unknown",
			Fn:          env.trailSearch,
		},
		{
			Namespace: "graph", Name: "search",
			Description: "Find code by meaning or name in the codebase knowledge graph (graphify-out/graph.json): functions, types, files, doc sections. Returns file:line so you read only that span, not whole files.",
			Params: []Param{
				{Name: "q", Type: "string", Doc: "question or identifiers, e.g. \"where are budget alerts raised\""},
				{Name: "k", Type: "number", Optional: true, Doc: fmt.Sprintf("results, default %d, max %d", defaultHits, maxHits)},
				{Name: "mode", Type: `"hybrid" | "semantic" | "lexical"`, Optional: true, Doc: "default: semantic when embeddings work (local Ollama if configured, else the agent's LLM provider), otherwise lexical with a note; lexical is best for exact identifiers"},
			},
			Returns: "{ mode: string; embedder?: string; note?: string; hits: { id: string; label: string; symbol?: string; file?: string; line?: number; doc?: string; community: number; score: number }[] }",
			Fn:      env.graphSearch,
		},
		{
			Namespace: "graph", Name: "neighbors",
			Description: "Nodes linked to a graph node (callers, callees, containers, references), breadth-first. dir \"out\" means id -> node.",
			Params: []Param{
				{Name: "id", Type: "string", Doc: "node id from graph.search"},
				{Name: "depth", Type: "number", Optional: true, Doc: "hops, default 1, max 3"},
				{Name: "limit", Type: "number", Optional: true, Doc: fmt.Sprintf("default %d, max %d", defaultHits*3, maxRecent)},
			},
			Returns: "{ id: string; label: string; symbol?: string; file?: string; line?: number; relation: string; dir: \"in\" | \"out\"; depth: number }[]",
			Fn:      env.graphNeighbors,
		},
		{
			Namespace: "practice", Name: "list",
			Description: "Practice problems in practice-problems/: id, skill and goal.",
			Returns:     "{ id: string; skill: string; goal: string }[]",
			Fn:          env.practiceList,
		},
		{
			Namespace: "practice", Name: "run",
			Description: "Run one practice problem's test command in the repo and report pass/fail with output.",
			Params:      []Param{{Name: "id", Type: "string", Doc: "problem id from practice.list"}},
			Returns:     "{ id: string; pass: boolean; exit_code: number; output: string; error?: string }",
			Fn:          env.practiceRun,
		},
		{
			Namespace: "context", Name: "compile",
			Description: "Compile the harness context (system prompt) for a mode/level/domain, e.g. to brief a subagent.",
			Params: []Param{
				{Name: "mode", Type: `"architect" | "build" | "explain"`, Optional: true},
				{Name: "level", Type: "string", Optional: true, Doc: "default: harness level"},
				{Name: "domain", Type: "string", Optional: true, Doc: "lessons domain, e.g. \"go\""},
			},
			Returns: "{ version: string; system: string; sources: string[] }",
			Fn:      env.compileContext,
		},
	}
}

func (env Env) recentEvents(_ context.Context, args map[string]any) (any, error) {
	n := defaultRecent
	if v, ok := args["n"].(float64); ok {
		n = int(v)
	}
	if n < 1 || n > maxRecent {
		return nil, fmt.Errorf("n must be 1..%d", maxRecent)
	}
	want, _ := args["ev"].(string)
	all, err := env.events()
	if err != nil {
		return nil, err
	}
	out := []events.Event{}
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		if want == "" || all[i].Ev == want {
			out = append(out, all[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (env Env) budgetStatus(context.Context, map[string]any) (any, error) {
	all, err := env.events()
	if err != nil {
		return nil, err
	}
	t := budget.New(env.Cfg.Budgets.PerFeatureUSD, env.Cfg.Budgets.AlertAt)
	for _, ev := range all {
		if ev.Ev == "cost" {
			amt, _ := ev.Data["amount"].(float64)
			t.Add(amt)
		}
	}
	return t.Status(), nil
}

func (env Env) reviewSessions(context.Context, map[string]any) (any, error) {
	for _, t := range env.Cfg.Tools {
		if len(t.SessionsFrom) > 0 {
			return discovery.SessionsFromCommand(t.SessionsFrom)
		}
	}
	return nil, errors.New("no tool in harness.json declares sessions_from")
}

func (env Env) trailSearch(ctx context.Context, args map[string]any) (any, error) {
	q, _ := args["q"].(string)
	if strings.TrimSpace(q) == "" {
		return nil, errors.New("q is required")
	}
	base := "http://127.0.0.1:3030"
	for _, t := range env.Cfg.Tools {
		if t.ID == "trail" && t.Health != "" {
			base = strings.TrimRight(t.Health, "/")
		}
	}
	u, err := url.Parse(base)
	if err != nil || !scope.Loopback([]string{u.Hostname()}) {
		return nil, fmt.Errorf("trail address %q is not loopback", base)
	}
	u.Path, u.RawQuery = "/api/v1/sessions/search", url.Values{"q": {q}}.Encode()
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Agent Trail not reachable (is the observe profile up?): %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Agent Trail HTTP %d", resp.StatusCode)
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("Agent Trail returned non-JSON: %w", err)
	}
	return out, nil
}

// Graph loads the code graph.
func (env Env) Graph() (*graph.Graph, error) { return env.graph() }

func (env Env) graph() (*graph.Graph, error) {
	return graph.Load(filepath.Join(env.Root, "graphify-out", "graph.json"), env.Root)
}

// Embedder picks the embeddings for graph search, in order:
//  1. harness.json "embeddings": a local (loopback) Ollama server, opt-in;
//  2. the harness agent's active LLM provider (.harness/llm.json), when it
//     has an embeddings API and a saved key, the same trust as chat;
//  3. none (nil): graph search is keyword-only. Nothing local is required.
func (env Env) Embedder() (graph.Embedder, error) {
	if e := env.Cfg.Embeddings; e != nil {
		return graph.NewOllama(e.URL, e.Model, e.QueryPrefix, e.DocPrefix)
	}
	cfg, keys := llm.ReadSaved(filepath.Join(env.Root, ".harness", "llm.json"))
	p, ok := llm.Lookup(cfg.Provider)
	if !ok || p.EmbedModel == "" || keys[p.ID] == "" {
		return nil, nil
	}
	base := p.BaseURL
	if cfg.BaseURL != "" {
		base = cfg.BaseURL
	}
	return graph.NewAPI(base, keys[p.ID], p.EmbedModel, p.EmbedInputType)
}

type searchResult struct {
	Mode     string      `json:"mode"`
	Embedder string      `json:"embedder,omitempty"`
	Note     string      `json:"note,omitempty"` // why the default fell back to keyword search
	Hits     []graph.Hit `json:"hits"`
}

func nonNil(h []graph.Hit) []graph.Hit {
	if h == nil {
		return []graph.Hit{}
	}
	return h
}

// EmbedCache is where node vectors are cached between runs.
func (env Env) EmbedCache() string {
	return filepath.Join(env.Root, ".harness", "cache", "graph-embeddings")
}

func (env Env) graphSearch(ctx context.Context, args map[string]any) (any, error) {
	q, _ := args["q"].(string)
	if strings.TrimSpace(q) == "" {
		return nil, errors.New("q is required")
	}
	k, err := intArg(args, "k", defaultHits, 1, maxHits)
	if err != nil {
		return nil, err
	}
	mode, _ := args["mode"].(string)
	g, err := env.graph()
	if err != nil {
		return nil, err
	}
	emb, embErr := env.Embedder()
	if mode != "" { // an explicit mode is honoured or fails loudly
		if embErr != nil && mode != "lexical" {
			return nil, embErr
		}
		hits, err := g.Find(ctx, q, k, mode, emb, env.EmbedCache())
		if err != nil {
			return nil, err
		}
		return searchResult{Mode: mode, Hits: nonNil(hits)}, nil
	}
	// Default: semantic when embeddings work (it beat hybrid in every recall run), else keyword search, saying why.
	note := "no embeddings available (the active LLM provider has no embeddings API, or no key is saved); keyword search only"
	if embErr != nil {
		note = "embeddings misconfigured: " + embErr.Error() + "; keyword search only"
	} else if emb != nil {
		hits, err := g.Find(ctx, q, k, "semantic", emb, env.EmbedCache())
		if err == nil {
			return searchResult{Mode: "semantic", Embedder: emb.Name(), Hits: nonNil(hits)}, nil
		}
		note = "embeddings failed (" + err.Error() + "); keyword search only"
	}
	return searchResult{Mode: "lexical", Note: note, Hits: nonNil(g.Search(q, k))}, nil
}

func (env Env) graphNeighbors(_ context.Context, args map[string]any) (any, error) {
	id, _ := args["id"].(string)
	depth, err := intArg(args, "depth", 1, 1, 3)
	if err != nil {
		return nil, err
	}
	limit, err := intArg(args, "limit", defaultHits*3, 1, maxRecent)
	if err != nil {
		return nil, err
	}
	g, err := env.graph()
	if err != nil {
		return nil, err
	}
	return g.Neighbors(id, depth, limit)
}

func intArg(args map[string]any, name string, def, lo, hi int) (int, error) {
	v, ok := args[name].(float64)
	if !ok {
		return def, nil
	}
	if n := int(v); float64(n) == v && n >= lo && n <= hi {
		return n, nil
	}
	return 0, fmt.Errorf("%s must be an integer %d..%d", name, lo, hi)
}

func (env Env) practiceList(context.Context, map[string]any) (any, error) {
	paths, err := filepath.Glob(filepath.Join(env.Root, "practice-problems", "*.json"))
	if err != nil {
		return nil, err
	}
	type item struct {
		ID    string `json:"id"`
		Skill string `json:"skill"`
		Goal  string `json:"goal"`
	}
	out := []item{}
	for _, p := range paths {
		pr, err := practice.Load(p)
		if err != nil {
			return nil, err
		}
		out = append(out, item{pr.ID, pr.Skill, pr.Goal})
	}
	return out, nil
}

func (env Env) practiceRun(ctx context.Context, args map[string]any) (any, error) {
	id, _ := args["id"].(string)
	if !problemID.MatchString(id) {
		return nil, fmt.Errorf("invalid problem id %q", id)
	}
	pr, err := practice.Load(filepath.Join(env.Root, "practice-problems", id+".json"))
	if err != nil {
		return nil, err
	}
	if pr.Cwd == "" || !filepath.IsAbs(pr.Cwd) {
		pr.Cwd = filepath.Join(env.Root, pr.Cwd)
	}
	return pr.RunContext(ctx), nil
}

func (env Env) compileContext(_ context.Context, args map[string]any) (any, error) {
	str := func(k string) string { s, _ := args[k].(string); return s }
	level := str("level")
	if level == "" {
		level = env.Cfg.Level
	}
	c := &compiler.Compiler{Root: env.Root, Cfg: env.Cfg, Defaults: harnessctx.FS}
	b, err := c.Compile(compiler.Request{Mode: str("mode"), Level: level, Domain: str("domain")})
	if err != nil {
		return nil, err
	}
	return map[string]any{"version": b.Version, "system": b.System(), "sources": b.Sources}, nil
}

// events reads the shared log; no log yet means no events.
func (env Env) events() ([]events.Event, error) {
	all, err := events.ReadAll(filepath.Join(env.Root, ".harness", "events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return all, err
}

func readJSON(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}
