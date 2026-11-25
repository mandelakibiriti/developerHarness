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
