// Package supervisor starts and stops the tools the harness owns. Each tool
// runs in its own process group so a stop signal reaches its children
// (npm -> next, ./start.sh -> python, etc.).
package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/devex/harness/internal/config"
)

// Process is one managed tool process.
type Process struct {
	tool config.Tool
	cmd  *exec.Cmd

	mu   sync.Mutex
	done chan struct{}
	err  error
}

// Start launches tool.Command in its own process group, logging to
// logDir/<id>.log.
func Start(tool config.Tool, logDir string) (*Process, error) {
	cmd := exec.Command(tool.Command[0], tool.Command[1:]...)
	if tool.Cwd != "" {
		cmd.Dir = tool.Cwd
	}
	cmd.Env = append(os.Environ(), flattenEnv(tool.Env)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(logDir, tool.ID+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = f
	cmd.Stderr = f

	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, fmt.Errorf("start %s: %w", tool.ID, err)
	}

	p := &Process{tool: tool, cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		f.Close()
		close(p.done)
	}()
	return p, nil
}

func flattenEnv(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// Done is closed when the process exits.
func (p *Process) Done() <-chan struct{} { return p.done }

// Pid is the process id (also the process-group id).
func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Running reports whether the process has not yet exited.
func (p *Process) Running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// Err returns the exit error once the process has finished.
func (p *Process) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Stop sends SIGTERM to the whole process group, waits up to grace, then SIGKILLs.
func (p *Process) Stop(grace time.Duration) error {
	pgid := p.cmd.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(grace):
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	_ = p.cmd.Process.Kill()
	<-p.done
	return fmt.Errorf("%s did not exit within %s; killed", p.tool.ID, grace)
}
