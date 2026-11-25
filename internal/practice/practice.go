// Package practice runs a practice problem: a small JSON file whose "process"
// step is a real command in the repo (go test, tsc --noEmit, pytest, ...).
// Pass = exit 0. This is the harness-side runner behind the LearnHarness plugin
// (P4); the problem files live in practice-problems/.
package practice

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Problem struct {
	ID      string   `json:"id"`
	Skill   string   `json:"skill"`
	Goal    string   `json:"goal"`
	Cwd     string   `json:"cwd,omitempty"`
	Setup   []string `json:"setup,omitempty"`
	Process []string `json:"process"`
	Timeout string   `json:"timeout,omitempty"`
}

type Result struct {
	ID       string `json:"id"`
	Pass     bool   `json:"pass"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// Load reads and sanity-checks a problem file.
func Load(path string) (*Problem, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Problem
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, fmt.Errorf("problem %s has empty id", path)
	}
	if len(p.Process) == 0 {
		return nil, fmt.Errorf("problem %q has empty process", p.ID)
	}
	return &p, nil
}

func (p *Problem) timeout() time.Duration {
	if p.Timeout == "" {
		return 2 * time.Minute
	}
	if d, err := time.ParseDuration(p.Timeout); err == nil {
		return d
	}
	return 2 * time.Minute
}

// Run executes setup (best-effort) then process, returning pass = exit 0.
func (p *Problem) Run() Result { return p.RunContext(context.Background()) }

// RunContext is Run, also stopped when ctx is done (e.g. a Code Mode deadline).
func (p *Problem) RunContext(ctx context.Context) Result {
	res := Result{ID: p.ID}
	if len(p.Setup) > 0 {
		if out, err := run(ctx, p.Setup, p.Cwd, p.timeout()); err != nil {
			res.Error = fmt.Sprintf("setup failed: %v: %s", err, strings.TrimSpace(out))
		}
	}
	out, err := run(ctx, p.Process, p.Cwd, p.timeout())
	res.Output = strings.TrimSpace(out)
	if ee, ok := err.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
		res.Pass = false
		return res
	}
	if err != nil {
		res.Error = err.Error()
		res.Pass = false
		return res
	}
	res.Pass = true
	return res
}

func run(ctx context.Context, args []string, cwd string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
