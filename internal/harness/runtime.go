// Package harness is the runtime that ties the pieces together: it starts and
// stops the tools for the selected profile, discovers session-shaped tools'
// addresses, health-checks everything, and serves the shell + subdomain proxy
// + SSE timeline on one port.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devex/harness/internal/budget"
	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/discovery"
	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/llm"
	"github.com/devex/harness/internal/proxy"
	"github.com/devex/harness/internal/scope"
	"github.com/devex/harness/internal/supervisor"
	"github.com/devex/harness/internal/track"
	"github.com/devex/harness/web"
)

// ToolState is the live status of one tool, served to the shell.
type ToolState struct {
	ID       string   `json:"id"`
	Tab      string   `json:"tab"`
	Profile  string   `json:"profile"`
	Managed  bool     `json:"managed"`
	Upstream string   `json:"upstream,omitempty"`
	Embed    string   `json:"embed,omitempty"` // themed proxy address to frame instead
	Status   string   `json:"status"`
	Healthy  bool     `json:"healthy"`
	Error    string   `json:"error,omitempty"`
	Pid      int      `json:"pid,omitempty"`
	Sessions []string `json:"sessions,omitempty"`
}

// Runtime owns the tools' lifecycle and the HTTP surface.
type Runtime struct {
	cfg     *config.Config
	profile string
	home    string
	log     *events.Log
	track   *track.Track

	mu     sync.Mutex
	tools  []config.Tool
	order  []string
	states map[string]*ToolState
	procs  map[string]*supervisor.Process

	budget     *budget.Tracker
	budgetStop chan struct{}
	scopeDesc  string

	root       string
	configPath string
	llmCfg     *llm.Config
	llmKeys    map[string]string     // provider id -> API key
	llmAvail   map[string]availEntry // provider id -> last key check
	reviewMu   sync.Mutex
	review     reviewState // latest Plannotator spec review
	llmMu      sync.Mutex
}

// New builds a Runtime for the given profile.
func New(cfg *config.Config, profile, home string, log *events.Log) *Runtime {
	return &Runtime{
		cfg:     cfg,
		profile: profile,
		home:    home,
		log:     log,
		states:  map[string]*ToolState{},
		procs:   map[string]*supervisor.Process{},
	}
}

// SetTrack attaches the level's track (tabs to show/hide, gates, agent mode).
func (rt *Runtime) SetTrack(tr *track.Track) {
	rt.track = tr
}

// SetProject records the project root (for docs/, floorplan/, graphify-out/)
// and the harness.json path (for persisting the level toggle).
func (rt *Runtime) SetProject(root, configPath string) {
	rt.root = root
	rt.configPath = configPath
}

// Scope reports the validated ARTEX scope description, or "" when the profile
// has no scoped (offensive) tool.
func (rt *Runtime) Scope() string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.scopeDesc
}

// Start begins every active tool. Managed tools are launched; unmanaged
// (session-shaped or desktop) tools are only discovered. A failing tool marks
// its tab red; the others carry on.
func (rt *Runtime) Start() error {
	tools, err := rt.cfg.ActiveTools(rt.profile)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	rt.tools = tools
	for _, t := range tools {
		rt.states[t.ID] = &ToolState{ID: t.ID, Tab: t.Tab, Profile: t.Profile, Managed: t.IsManaged(), Status: "stopped"}
		if t.Theme == config.ThemeShadcn {
			rt.states[t.ID].Embed = rt.embedURL(t.ID)
		}
	}
	rt.mu.Unlock()

	rt.budget = budget.New(rt.cfg.Budgets.PerFeatureUSD, rt.cfg.Budgets.AlertAt)
	rt.startBudgetWatcher()
	rt.llmCfg, rt.llmKeys = rt.readLLMFromDisk()
	rt.llmAvail = map[string]availEntry{}
	for _, t := range tools {
		if t.Scope != nil {
			if desc, err := scope.Validate(t.Scope.Allow, t.Scope.Ports, t.Scope.Deny); err != nil {
				rt.setScope("INVALID: " + err.Error())
			} else {
				rt.setScope(desc)
			}
		}
	}

	for _, t := range tools {
		rt.resolveUpstream(t)
		if !t.IsManaged() {
			rt.setStatus(t.ID, "not-managed")
			continue
		}
		// something already answers (e.g. started by hand): use it rather than fail on a busy port
		if t.Health != "" && t.Scope == nil && httpGetOK(t.Health) {
			rt.setStatus(t.ID, "external")
			continue
		}
		if err := rt.startTool(t); err != nil {
			rt.log.Append(events.Event{Ev: "tool-error", Tool: t.ID, Data: map[string]any{"error": err.Error()}})
			rt.setError(t.ID, err)
			continue
		}
		rt.mu.Lock()
		rt.order = append(rt.order, t.ID)
		rt.mu.Unlock()
		rt.log.Append(events.Event{Ev: "tool-start", Tool: t.ID})
	}
	return nil
}

func (rt *Runtime) startTool(t config.Tool) error {
	if t.Scope != nil {
		desc, err := scope.Validate(t.Scope.Allow, t.Scope.Ports, t.Scope.Deny)
		if err != nil {
			return fmt.Errorf("refusing to start %s: %v", t.ID, err)
		}
		rt.setScope(desc)
		if !rt.scopeConfirmed() {
			return fmt.Errorf("refusing to start %s: scope mapping not confirmed (review it, then run `harness scope --confirm`): %s", t.ID, desc)
		}
	}
	for _, d := range t.DependsOn {
		if d == "docker" && !dockerRunning() {
			return fmt.Errorf("docker is not running (required by %s)", t.ID)
		}
	}
	if t.LoopbackOnly {
		var err error
		if t, err = rt.loopbackOnly(t); err != nil {
			return fmt.Errorf("refusing to start %s without loopback restriction: %w", t.ID, err)
		}
	}
	proc, err := supervisor.Start(t, filepath.Join(rt.home, "logs"))
	if err != nil {
		return err
	}
	rt.mu.Lock()
	rt.procs[t.ID] = proc
	rt.states[t.ID].Pid = proc.Pid()
	rt.states[t.ID].Status = "starting"
	rt.mu.Unlock()
	return nil
}

func dockerRunning() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "info").Run() == nil
}

// startBudgetWatcher subscribes to the shared log and turns cost events into
// budget-alert events when a threshold is crossed for the first time.
func (rt *Runtime) startBudgetWatcher() {
	rt.budgetStop = make(chan struct{})
	id, ch := rt.log.Subscribe()
	go func() {
		defer rt.log.Unsubscribe(id)
		for {
			select {
			case <-rt.budgetStop:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if ev.Ev != "cost" {
					continue
				}
				amt, _ := ev.Data["amount"].(float64)
				for _, a := range rt.budget.Add(amt) {
					rt.log.Append(events.Event{
						Ev:   "budget-alert",
						Tool: "spend",
						Data: map[string]any{
							"threshold":       a,
							"spent":           rt.budget.Status().Spent,
							"per_feature_usd": rt.budget.Status().PerFeature,
						},
					})
				}
			}
		}
	}()
}

func (rt *Runtime) scopeConfirmed() bool {
	_, err := os.Stat(filepath.Join(rt.home, "scope.confirmed"))
	return err == nil
}

func (rt *Runtime) setScope(desc string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.scopeDesc = desc
}

// Run starts the health/discovery loop until ctx is cancelled.
func (rt *Runtime) Run(ctx context.Context) {
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				rt.poll()
			}
		}
	}()
}

func (rt *Runtime) poll() {
	rt.mu.Lock()
	tools := append([]config.Tool(nil), rt.tools...)
	rt.mu.Unlock()

	for _, t := range tools {
		rt.resolveUpstream(t)
		if t.IsManaged() {
			rt.mu.Lock()
			proc, ok := rt.procs[t.ID]
			rt.mu.Unlock()
			if ok && !proc.Running() {
				if err := proc.Err(); err != nil {
					rt.setError(t.ID, fmt.Errorf("exited: %w", err))
				} else {
					rt.setError(t.ID, fmt.Errorf("exited"))
				}
				continue
			}
		}
		if up := rt.upstream(t.ID); up != "" {
			rt.setHealthy(t.ID, httpGetOK(up))
		}
	}
}

// resolveUpstream points a tool at its address: a fixed health URL for
// servers/desktop apps, or a discovered port for session-shaped tools.
func (rt *Runtime) resolveUpstream(t config.Tool) {
	if t.Health != "" && t.PortFrom == nil {
		rt.setUpstream(t.ID, t.Health)
	}
	if t.PortFrom != nil {
		var up string
		var err error
		switch {
		case t.PortFrom.File != "":
			up, err = discovery.FromFile(t.PortFrom.File, t.PortFrom.Regex)
		case len(t.PortFrom.Command) > 0:
			up, err = discovery.FromCommand(t.PortFrom.Command, t.PortFrom.JSONPath)
		}
		if err == nil && up != "" {
			rt.setUpstream(t.ID, up)
		}
	}
	if len(t.SessionsFrom) > 0 {
		if s, err := discovery.SessionsFromCommand(t.SessionsFrom); err == nil {
			rt.setSessions(t.ID, s)
		}
	}
}

// Handler serves the shell, status API, SSE timeline and the subdomain proxy.
func (rt *Runtime) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(web.FS)))
	mux.HandleFunc("/api/status", rt.handleStatus)
	mux.Handle("/events", rt.log)
	mux.HandleFunc("/api/llm", rt.handleLLM)
	mux.HandleFunc("/api/chat", rt.handleChat)
	mux.HandleFunc("/api/docs", rt.handleDocs)
	mux.HandleFunc("/api/docs/generate", rt.handleDocsGenerate)
	mux.HandleFunc("/api/level", rt.handleLevel)
	mux.HandleFunc("/api/floorplan", rt.handleFloorplan)
	mux.HandleFunc("/api/floorplan/generate", rt.handleFloorplanGenerate)
	mux.HandleFunc("/api/graph", rt.handleGraph)
	mux.HandleFunc("/graph/view", rt.handleGraphView)
	mux.HandleFunc("/api/graph/query", rt.handleGraphQuery)
	mux.HandleFunc("/api/review", rt.handleReview)
	mux.HandleFunc("/api/llm/test", rt.handleLLMTest)
	mux.HandleFunc("/api/llm/models", rt.handleLLMModels)
	mux.HandleFunc("/api/llm/available", rt.handleLLMAvailable)
	mux.HandleFunc("/api/files", rt.handleFiles)
	mux.HandleFunc("/api/terminal", rt.handleTerminal)
	mux.HandleFunc("/api/run", rt.handleRun)
	mux.HandleFunc("/api/apps", rt.handleApps)
	mux.HandleFunc("/api/evals", rt.handleEvals)
	mux.HandleFunc("/api/telemetry", rt.handleTelemetry)
	_, port, _ := net.SplitHostPort(rt.cfg.Listen)
	return proxy.Handler(rt.resolve, proxy.SameOrigin(port, mux), rt.themeSnippet)
}

// resolve maps a subdomain (tool id) to its current upstream URL.
func (rt *Runtime) resolve(sub string) (string, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	st, ok := rt.states[sub]
	if !ok || st.Upstream == "" {
		return "", false
	}
	return st.Upstream, true
}

type statusResp struct {
	Running bool           `json:"running"`
	Project string         `json:"project"` // folder this instance tracks
	Level   string         `json:"level"`
	Profile string         `json:"profile"`
	Listen  string         `json:"listen"`
	Tools   []ToolState    `json:"tools"`
	Track   *trackInfo     `json:"track,omitempty"`
	Budget  *budget.Status `json:"budget,omitempty"`
	Scope   string         `json:"scope,omitempty"`
}

type trackInfo struct {
	Level             string   `json:"level"`
	Profile           string   `json:"profile"`
	PrimaryTabs       []string `json:"primary_tabs"`
	HiddenTabs        []string `json:"hidden_tabs"`
	Gates             []string `json:"gates"`
	FloorplanAudience string   `json:"floorplan_audience"`
	AgentMode         string   `json:"agent_mode"`
}

func (rt *Runtime) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := statusResp{
		Running: true, Project: rt.root,
		Level:   rt.cfg.Level,
		Profile: rt.profile,
		Listen:  rt.cfg.Listen,
		Tools:   rt.Status(),
		Scope:   rt.Scope(),
	}
	if rt.track != nil {
		resp.Track = &trackInfo{
			Level:             rt.track.Level,
			Profile:           rt.track.Profile,
			PrimaryTabs:       rt.track.PrimaryTabs,
			HiddenTabs:        rt.track.HiddenTabs,
			Gates:             rt.track.Gates,
			FloorplanAudience: rt.track.FloorplanAudience,
			AgentMode:         rt.track.AgentMode,
		}
	}
	if rt.budget != nil {
		b := rt.budget.Status()
		resp.Budget = &b
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Status returns a snapshot of every active tool's state.
func (rt *Runtime) Status() []ToolState {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]ToolState, 0, len(rt.tools))
	for _, t := range rt.tools {
		if st, ok := rt.states[t.ID]; ok {
			out = append(out, *st)
		}
	}
	return out
}

// Stop tears down managed tools in reverse start order, then runs any explicit
// stop commands (docker compose stop).
func (rt *Runtime) Stop() {
	if rt.budgetStop != nil {
		close(rt.budgetStop)
	}
	rt.mu.Lock()
	order := append([]string(nil), rt.order...)
	rt.mu.Unlock()

	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		rt.mu.Lock()
		proc := rt.procs[id]
		rt.mu.Unlock()
		if proc == nil {
			continue
		}
		rt.log.Append(events.Event{Ev: "tool-stop", Tool: id})
		_ = proc.Stop(5 * time.Second)
	}
	for _, t := range rt.tools {
		if len(t.Stop) > 0 {
			_ = exec.Command(t.Stop[0], t.Stop[1:]...).Run()
		}
	}
}

// --- state helpers ---

func (rt *Runtime) setStatus(id, status string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if st, ok := rt.states[id]; ok {
		st.Status = status
	}
}

func (rt *Runtime) setHealthy(id string, ok bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	st, found := rt.states[id]
	if !found {
		return
	}
	st.Healthy = ok
	if ok && st.Status != "error" {
		st.Status = "healthy"
	} else if !ok && st.Status == "healthy" {
		st.Status = "starting"
	}
}

func (rt *Runtime) setUpstream(id, up string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if st, ok := rt.states[id]; ok {
		st.Upstream = up
	}
}

func (rt *Runtime) setSessions(id string, s []string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if st, ok := rt.states[id]; ok {
		st.Sessions = s
	}
}

func (rt *Runtime) setError(id string, err error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if st, ok := rt.states[id]; ok {
		st.Status = "error"
		st.Error = err.Error()
	}
}

func (rt *Runtime) upstream(id string) string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if st, ok := rt.states[id]; ok {
		return st.Upstream
	}
	return ""
}

func httpGetOK(url string) bool {
	if url == "" || !strings.HasPrefix(url, "http") {
		return false
	}
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}
