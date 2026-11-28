// Package config loads and validates harness.json, the single source of truth
// for the tools, pinned versions, ports, profiles, level and budgets.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SchemaVersion is the only accepted value for the "$schema" field.
const SchemaVersion = "harness@1"

// ProfileRank orders profiles from smallest footprint to largest. A tool whose
// profile ranks <= the selected profile's rank is active.
var ProfileRank = map[string]int{"core": 0, "observe": 1, "security": 2}

type Config struct {
	Schema  string   `json:"$schema"`
	Level   string   `json:"level"`
	Profile string   `json:"profile"`
	Listen  string   `json:"listen"`
	Project Project  `json:"project"`
	Tools   []Tool   `json:"tools"`
	Budgets Budgets  `json:"budgets"`
	Rules   []string `json:"rules"`
	// Learn is the improve loop's edit boundary (Hermit's "template"): which
	// repo-relative files a Proposer may touch. See Learnable.
	Learn Learn `json:"learn"`
	// Improve tunes the improve loop's proposer (method, candidates, model).
	Improve Improve `json:"improve"`
	// Embeddings turns on semantic code-graph search; omit for keyword search only.
	Embeddings *Embeddings `json:"embeddings,omitempty"`
}

// Embeddings is a local embedding server (Ollama's /api/embed); it must be
// loopback, like every other tool. Prefixes are the model's task markers,
// e.g. nomic-embed-text's "search_query: " / "search_document: ".
type Embeddings struct {
	URL         string `json:"url"`
	Model       string `json:"model"`
	QueryPrefix string `json:"query_prefix,omitempty"`
	DocPrefix   string `json:"doc_prefix,omitempty"`
}

type Project struct {
	Root     string `json:"root"`
	AppPorts []int  `json:"app_ports"`
}

type Budgets struct {
	PerFeatureUSD float64   `json:"per_feature_usd"`
	AlertAt       []float64 `json:"alert_at"`
}

// Improve configures the improve loop's improver role — distinct from the
// executor (the developer's Claude Code) and the judge (deterministic Go in
// internal/improve). Proposer names the improve method, resolved by
// internal/improve.ResolveProposer.
type Improve struct {
	Proposer string `json:"proposer"`          // method name; default "claude"
	K        int    `json:"k"`                 // candidates per round; default 3, range 1..50
	Model    string `json:"model,omitempty"`   // optional model override for the proposer
	Prompt   string `json:"prompt,omitempty"`  // optional prompt-template override
	Timeout  string `json:"timeout,omitempty"` // proposer call deadline; default 5m
}

type Tool struct {
	ID           string            `json:"id"`
	Tab          string            `json:"tab"`
	Profile      string            `json:"profile"`
	Version      string            `json:"version,omitempty"`
	Managed      *bool             `json:"managed,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Health       string            `json:"health,omitempty"`
	PortFrom     *PortFrom         `json:"port_from,omitempty"`
	SessionsFrom []string          `json:"sessions_from,omitempty"`
	Stop         []string          `json:"stop,omitempty"`
	DependsOn    []string          `json:"depends_on,omitempty"`
	Scope        *Scope            `json:"scope,omitempty"`
	Note         string            `json:"note,omitempty"`
	Theme        string            `json:"theme,omitempty"`         // "shadcn": restyle through the proxy
	LoopbackOnly bool              `json:"loopback_only,omitempty"` // Node tools: pin listeners to 127.0.0.1
	Timeout      string            `json:"timeout,omitempty"`
}

type PortFrom struct {
	File     string   `json:"file,omitempty"`
	Regex    string   `json:"regex,omitempty"`
	Command  []string `json:"command,omitempty"`
	JSONPath string   `json:"json_path,omitempty"`
}

type Scope struct {
	Allow []string `json:"allow,omitempty"`
	Ports []int    `json:"ports,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// IsManaged reports whether the harness owns this tool's lifetime. A nil
// "managed" field means managed (the default).
func (t Tool) IsManaged() bool {
	if t.Managed == nil {
		return true
	}
	return *t.Managed
}

// HealthTimeout is the max time to wait for the tool's health URL before
// marking its tab red. Defaults to 30s.
func (t Tool) HealthTimeout() time.Duration {
	if t.Timeout == "" {
		return 30 * time.Second
	}
	if d, err := time.ParseDuration(t.Timeout); err == nil {
		return d
	}
	return 30 * time.Second
}

// ToolsDir is where tool sources live (the ${HARNESS_TOOLS} template value).
func ToolsDir() string {
	if d := os.Getenv("HARNESS_TOOLS"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".harness", "tools")
}

// Expand replaces ${VAR} in s using the environment, plus ${HARNESS_TOOLS}.
func Expand(s, toolsDir string) string {
	re := regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	return re.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		if name == "HARNESS_TOOLS" {
			return toolsDir
		}
		if v := os.Getenv(name); v != "" {
			return v
		}
		return m
	})
}

// Load reads, expands and validates a harness.json file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	td := ToolsDir()
	for i := range c.Tools {
		c.Tools[i].Cwd = Expand(c.Tools[i].Cwd, td)
		for k, v := range c.Tools[i].Env {
			c.Tools[i].Env[k] = Expand(v, td)
		}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// ThemeShadcn is the only built-in tool theme.
const ThemeShadcn = "shadcn"

// Validate checks the schema, fills defaults, and rejects dependency cycles.
func (c *Config) Validate() error {
	if c.Schema != SchemaVersion {
		return fmt.Errorf("unsupported schema %q (want %q)", c.Schema, SchemaVersion)
	}
	if c.Profile == "" {
		c.Profile = "core"
	}
	if _, ok := ProfileRank[c.Profile]; !ok {
		return fmt.Errorf("unknown profile %q (want core|observe|security)", c.Profile)
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:4400"
	}
	if c.Level == "" {
		c.Level = "beginner"
	}
	if c.Project.Root == "" {
		c.Project.Root = "."
	}

	seen := map[string]bool{}
	for i := range c.Tools {
		t := &c.Tools[i]
		if t.ID == "" {
			return fmt.Errorf("tool #%d has empty id", i)
		}
		if t.Tab == "" {
			t.Tab = t.ID
		}
		if seen[t.ID] {
			return fmt.Errorf("duplicate tool id %q", t.ID)
		}
		seen[t.ID] = true
		if _, ok := ProfileRank[t.Profile]; !ok {
			return fmt.Errorf("tool %q has unknown profile %q (want core|observe|security)", t.ID, t.Profile)
		}
		if t.IsManaged() && len(t.Command) == 0 {
			return fmt.Errorf("managed tool %q has no command", t.ID)
		}
		if t.Health != "" && !strings.HasPrefix(t.Health, "http") {
			return fmt.Errorf("tool %q health %q is not an http(s) URL", t.ID, t.Health)
		}
		if t.Theme != "" && t.Theme != ThemeShadcn {
			return fmt.Errorf("tool %q has unknown theme %q (want %q)", t.ID, t.Theme, ThemeShadcn)
		}
	}
	if len(c.Learn.Allow) == 0 {
		c.Learn.Allow = append([]string{}, defaultLearnAllow...)
	}
	for _, pat := range append(append([]string{}, c.Learn.Allow...), c.Learn.Deny...) {
		if strings.TrimSpace(pat) == "" {
			return fmt.Errorf("learn glob must not be empty")
		}
		if _, err := globToRegex(pat); err != nil {
			return fmt.Errorf("learn glob %q: %w", pat, err)
		}
	}
	if c.Improve.Proposer == "" {
		c.Improve.Proposer = "claude"
	}
	if c.Improve.K == 0 {
		c.Improve.K = 3
	}
	if c.Improve.K < 1 || c.Improve.K > 50 {
		return fmt.Errorf("improve.k must be 1..50, got %d", c.Improve.K)
	}
	return checkDeps(c.Tools)
}

func checkDeps(tools []Tool) error {
	byID := map[string]Tool{}
	for _, t := range tools {
		byID[t.ID] = t
	}
	for _, t := range tools {
		for _, d := range t.DependsOn {
			if d == "docker" {
				continue // virtual dependency (a runtime, not a tool)
			}
			if _, ok := byID[d]; !ok {
				return fmt.Errorf("tool %q depends on unknown tool %q", t.ID, d)
			}
		}
	}
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("dependency cycle involving %q", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, d := range byID[id].DependsOn {
			if d == "docker" {
				continue
			}
			if err := visit(d); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, t := range tools {
		if err := visit(t.ID); err != nil {
			return err
		}
	}
	return nil
}

// ActiveTools returns the tools active in profile, topologically sorted so
// dependencies start before dependents.
func (c *Config) ActiveTools(profile string) ([]Tool, error) {
	rank, ok := ProfileRank[profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", profile)
	}
	var active []Tool
	for _, t := range c.Tools {
		if ProfileRank[t.Profile] <= rank {
			active = append(active, t)
		}
	}
	return TopoSort(active)
}

// TopoSort returns tools ordered by depends_on (dependencies first).
func TopoSort(tools []Tool) ([]Tool, error) {
	byID := map[string]*Tool{}
	for i := range tools {
		byID[tools[i].ID] = &tools[i]
	}
	visited := map[string]bool{}
	inStack := map[string]bool{}
	var order []Tool
	var visit func(id string) error
	visit = func(id string) error {
		if inStack[id] {
			return fmt.Errorf("dependency cycle at %q", id)
		}
		if visited[id] {
			return nil
		}
		inStack[id] = true
		t := byID[id]
		for _, d := range t.DependsOn {
			if _, ok := byID[d]; ok {
				if err := visit(d); err != nil {
					return err
				}
			}
		}
		inStack[id] = false
		visited[id] = true
		order = append(order, *t)
		return nil
	}
	for i := range tools {
		if err := visit(tools[i].ID); err != nil {
			return nil, err
		}
	}
	return order, nil
}
