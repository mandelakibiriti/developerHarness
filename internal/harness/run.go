package harness

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/proc"
)

// --- /api/run: execute the Explorer editor buffer ---
//
// Runs as the harness user with no sandbox (same trust level as /api/terminal);
// it is only reachable on loopback and SameOrigin refuses cross-site POSTs.

const (
	runTimeout   = 20 * time.Second // `go run` compiles first, so allow headroom
	runMaxCode   = 256 << 10
	runMaxOutput = 64 << 10
)

// runners maps an editor language to its file name and command.
var runners = map[string]struct {
	file string
	cmd  []string
}{
	"go":         {"main.go", []string{"go", "run", "main.go"}},
	"python":     {"main.py", []string{proc.Python(), "main.py"}},
	"javascript": {"main.js", []string{"node", "main.js"}},
}

type runRequest struct {
	Lang string `json:"lang"`
	Code string `json:"code"`
}

type runResult struct {
	Output     string `json:"output"`
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out"`
	Truncated  bool   `json:"truncated"`
	DurationMS int64  `json:"duration_ms"`
}

// capWriter keeps the first n bytes and drops the rest.
type capWriter struct {
	buf       []byte
	n         int
	truncated bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	room := c.n - len(c.buf)
	if room < len(p) {
		c.truncated = true
		if room > 0 {
			c.buf = append(c.buf, p[:room]...)
		}
		return len(p), nil
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}

func (rt *Runtime) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req runRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, runMaxCode+1024)).Decode(&req); err != nil {
		http.Error(w, "bad json or code too large", http.StatusBadRequest)
		return
	}
	if _, ok := runners[req.Lang]; !ok {
		http.Error(w, "unsupported language: use go, python or javascript", http.StatusBadRequest)
		return
	}
	res, err := runCode(r.Context(), req.Lang, req.Code, runTimeout)
	if err != nil {
		http.Error(w, "run failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rt.log.Append(events.Event{Ev: "run", Data: map[string]any{"lang": req.Lang, "exit": res.ExitCode, "ms": res.DurationMS}})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// runCode writes code into a fresh temp dir and runs it there. A non-zero exit
// is a normal result; err is only for failures to start (missing toolchain).
func runCode(ctx context.Context, lang, code string, timeout time.Duration) (runResult, error) {
	rn := runners[lang]
	dir, err := os.MkdirTemp("", "harness-run-")
	if err != nil {
		return runResult{}, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, rn.file), []byte(code), 0o600); err != nil {
		return runResult{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, rn.cmd[0], rn.cmd[1:]...)
	cmd.Dir = dir
	// Own process group so a timeout also kills children (`go run` execs the built binary).
	proc.Group(cmd)
	cmd.Cancel = func() error { return proc.Kill(cmd.Process.Pid) }
	cmd.WaitDelay = 2 * time.Second
	out := &capWriter{n: runMaxOutput}
	cmd.Stdout, cmd.Stderr = out, out

	start := time.Now()
	err = cmd.Run()
	res := runResult{
		Output:     string(out.buf),
		Truncated:  out.truncated,
		DurationMS: time.Since(start).Milliseconds(),
		TimedOut:   errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr) || res.TimedOut:
		res.ExitCode = cmd.ProcessState.ExitCode()
	default:
		return runResult{}, err
	}
	return res, nil
}
