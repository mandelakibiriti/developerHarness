// Package doctor runs preflight checks and returns one-line fixes for each
// failure: runtimes, PATH tools, Docker (security profile only), pinned tool
// commands, and free ports.
package doctor

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devex/harness/internal/config"
)

// Finding is the result of one check.
type Finding struct {
	Check  string `json:"check"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Run checks everything relevant to the given profile.
func Run(cfg *config.Config, profile string) []Finding {
	var out []Finding
	out = append(out,
		checkRuntime("node", ">=22", 22, 0),
		checkRuntime("python3", ">=3.10", 3, 10),
	)

	if config.ProfileRank[profile] >= config.ProfileRank["observe"] {
		out = append(out, onPath("agent-trail", "npm i -g @camtrik/agent-trail"))
	}
	out = append(out,
		onPath("claude", "install Claude Code: https://claude.com/code"),
		onPath("plannotator", "install Plannotator: https://github.com/backnotprop/plannotator"),
	)

	if config.ProfileRank[profile] >= config.ProfileRank["security"] {
		out = append(out, checkDocker())
	}

	out = append(out, checkPort(cfg.Listen))

	tools, err := cfg.ActiveTools(profile)
	if err != nil {
		out = append(out, Finding{Check: "tools", OK: false, Detail: err.Error(), Fix: "fix harness.json"})
		return out
	}
	for _, t := range tools {
		cmd := t.Command
		if len(cmd) == 0 && t.PortFrom != nil {
			cmd = t.PortFrom.Command
		}
		if len(cmd) == 0 {
			continue
		}
		name := cmd[0]
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "./") || strings.HasPrefix(name, "~/") {
			continue // absolute or relative path, not a PATH lookup
		}
		f := Finding{Check: "tool " + t.ID, OK: true}
		if _, err := exec.LookPath(name); err != nil {
			f.OK = false
			f.Detail = fmt.Sprintf("%q not on PATH", name)
			f.Fix = "install " + t.ID + " (needs " + name + ")"
		} else {
			f.Detail = name + " on PATH"
		}
		out = append(out, f)
	}
	// Floorplan (map tool) is a cloned repo with a serve.mjs entrypoint, not a
	// PATH binary — check its source is present.
	for _, t := range tools {
		if t.ID == "map" {
			serve := filepath.Join(config.ToolsDir(), "floorplan", "serve.mjs")
			f := Finding{Check: "tool map (floorplan source)", OK: true}
			if _, err := os.Stat(serve); err != nil {
				f.OK = false
				f.Detail = "floorplan not cloned to " + filepath.Join(config.ToolsDir(), "floorplan")
				f.Fix = "run `harness init`"
			} else {
				f.Detail = "floorplan source present"
			}
			out = append(out, f)
		}
	}
	return out
}

func checkRuntime(bin, want string, wantMajor, wantMinor int) Finding {
	f := Finding{Check: bin + " runtime", OK: true}
	path, err := exec.LookPath(bin)
	if err != nil {
		f.OK = false
		f.Detail = bin + " not on PATH"
		f.Fix = "install " + bin + " " + want
		return f
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		out, err = exec.Command(bin, "-v").Output()
	}
	if err != nil {
		f.OK = false
		f.Detail = "could not read " + bin + " version: " + err.Error()
		f.Fix = "ensure " + bin + " is installed (" + want + ")"
		return f
	}
	maj, min, err := parseVersion(strings.TrimSpace(string(out)))
	if err != nil || maj < wantMajor || (maj == wantMajor && min < wantMinor) {
		f.OK = false
		f.Detail = fmt.Sprintf("%s %s found; need %s", bin, strings.TrimSpace(string(out)), want)
		f.Fix = "upgrade " + bin + " to " + want
		return f
	}
	f.Detail = fmt.Sprintf("%s v%d.%d (%s)", bin, maj, min, path)
	return f
}

func onPath(bin, fix string) Finding {
	f := Finding{Check: bin, OK: true}
	path, err := exec.LookPath(bin)
	if err != nil {
		f.OK = false
		f.Detail = bin + " not on PATH"
		f.Fix = fix
		return f
	}
	f.Detail = bin + " at " + path
	return f
}

func checkDocker() Finding {
	f := Finding{Check: "docker", OK: true}
	if _, err := exec.LookPath("docker"); err != nil {
		f.OK = false
		f.Detail = "docker not on PATH"
		f.Fix = "install Docker Desktop"
		return f
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		f.OK = false
		f.Detail = "docker daemon not running"
		f.Fix = "start Docker Desktop"
		return f
	}
	f.Detail = "docker daemon running"
	return f
}

func checkPort(addr string) Finding {
	f := Finding{Check: "listen port " + addr, OK: true}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		f.OK = false
		f.Detail = "bad listen address " + addr
		f.Fix = "set listen to host:port"
		return f
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		f.OK = false
		f.Detail = "port " + port + " in use"
		f.Fix = "free port " + port + " or change listen"
		return f
	}
	ln.Close()
	f.Detail = "free"
	return f
}

func parseVersion(s string) (int, int, error) {
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	s = strings.TrimPrefix(s, "Python ")
	s = strings.SplitN(s, " ", 2)[0] // drop any trailing words
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("unexpected version %q", s)
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return maj, min, nil
}
