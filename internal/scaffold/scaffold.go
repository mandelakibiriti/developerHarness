// Package scaffold initialises a harness instance in a project folder and keeps
// a registry of instances, so one developer can run several side by side.
package scaffold

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	harnessassets "github.com/devex/harness"
)

// Instance ports step by ten so they never land on the tool ports (4401-4404).
const (
	firstPort = 4400
	portStep  = 10
	lastPort  = 4990
)

// Project is one registered harness instance.
type Project struct {
	Path string `json:"path"`
	Port int    `json:"port"`
}

// Home is where the registry lives (HARNESS_HOME overrides ~/.harness).
func Home() (string, error) {
	if h := os.Getenv("HARNESS_HOME"); h != "" {
		return h, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".harness"), nil
}

// Projects lists registered instances whose harness.json still exists.
func Projects() ([]Project, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(home, "projects.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all, live []Project
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("parse project registry: %w", err)
	}
	for _, p := range all {
		if _, err := os.Stat(filepath.Join(p.Path, "harness.json")); err == nil {
			live = append(live, p)
		}
	}
	return live, nil
}

func register(p Project) error {
	home, err := Home()
	if err != nil {
		return err
	}
	list, err := Projects()
	if err != nil {
		return err
	}
	out := []Project{p}
	for _, q := range list {
		if q.Path != p.Path {
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "projects.json"), append(b, '\n'), 0o600)
}

// freePort picks a port no other project claims and nothing is listening on.
func freePort(taken map[int]bool) (int, error) {
	for p := firstPort; p <= lastPort; p += portStep {
		if taken[p] {
			continue
		}
		if ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
			ln.Close()
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port between %d and %d; pass --port", firstPort, lastPort)
}

// rulesFor picks rule packs from the languages the project already has.
func rulesFor(dir string) []string {
	has := func(name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }
	rules := []string{}
	for _, r := range []struct{ marker, pack string }{
		{"go.mod", "rules/go.md"}, {"tsconfig.json", "rules/typescript.md"}, {"package.json", "rules/web.md"},
		{"pyproject.toml", "rules/python.md"}, {"requirements.txt", "rules/python.md"}, {"Cargo.toml", "rules/rust.md"},
	} {
		if has(r.marker) && !contains(rules, r.pack) {
			rules = append(rules, r.pack)
		}
	}
	return append(rules, "rules/api-design.md", "rules/architecture.md")
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

const claudeSettings = `{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write|Bash",
        "hooks": [
          {
            "type": "command",
            "command": "jq -c '{t:(now|todate), ev:.hook_event_name, session:.session_id, tool:.tool_name} + (if $ENV.HARNESS_RUN then {data:{run:$ENV.HARNESS_RUN}} else {} end)' >> \"${CLAUDE_PROJECT_DIR:-.}/.harness/events.jsonl\""
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "jq -c '{t:(now|todate), ev:\"stop\", session:.session_id} + (if $ENV.HARNESS_RUN then {data:{run:$ENV.HARNESS_RUN}} else {} end)' >> \"${CLAUDE_PROJECT_DIR:-.}/.harness/events.jsonl\""
          }
        ]
      }
    ]
  }
}
`

const mcpConfig = `{
  "mcpServers": {
    "harness": { "command": "harness", "args": ["mcp"] }
  }
}
`

// Result says what Init did.
type Result struct {
	Dir     string
	Port    int
	Written []string
	Kept    []string // existed already and were left untouched
}

// Init writes a harness instance into dir without overwriting anything, ignores
// .harness/ in git (it holds API keys and logs) and registers the project.
func Init(dir string, port int) (Result, error) {
	var res Result
	dir, err := filepath.Abs(dir)
	if err != nil {
		return res, err
	}
	if home, err := os.UserHomeDir(); err == nil && dir == home {
		return res, fmt.Errorf("refusing to initialise your home folder; run this inside a project folder")
	}
	if _, err := os.Stat(filepath.Join(dir, "harness.json")); err == nil {
		return res, fmt.Errorf("%s already has a harness.json", dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".harness"), 0o700); err != nil {
		return res, err
	}
	res.Dir = dir

	// ignore .harness/ before anything secret can land in it
	if err := ignore(filepath.Join(dir, ".gitignore"), ".harness/"); err != nil {
		return res, err
	}

	put := func(rel string, data []byte) error {
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if os.IsExist(err) {
			res.Kept = append(res.Kept, rel)
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		res.Written = append(res.Written, rel)
		_, err = f.Write(data)
		return err
	}

	err = fs.WalkDir(harnessassets.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == "harness.json" {
			return err
		}
		b, err := harnessassets.FS.ReadFile(p)
		if err != nil {
			return err
		}
		return put(p, b)
	})
	if err != nil {
		return res, err
	}
	if err := put(".claude/settings.json", []byte(claudeSettings)); err != nil {
		return res, err
	}
	if err := put(".mcp.json", []byte(mcpConfig)); err != nil {
		return res, err
	}

	others, err := Projects()
	if err != nil {
		return res, err
	}
	if port == 0 {
		taken := map[int]bool{}
		for _, p := range others {
			taken[p.Port] = true
		}
		if port, err = freePort(taken); err != nil {
			return res, err
		}
	}
	res.Port = port

	// harness.json goes last: its presence means the instance is complete.
	tmpl, err := harnessassets.FS.ReadFile("harness.json")
	if err != nil {
		return res, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(tmpl, &cfg); err != nil {
		return res, err
	}
	cfg["listen"] = "127.0.0.1:" + strconv.Itoa(port)
	cfg["rules"] = rulesFor(dir)
	cfg["level"], cfg["profile"] = "beginner", "core"
	cfg["project"] = map[string]any{"root": ".", "app_ports": []int{8080}}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return res, err
	}
	if err := put("harness.json", append(b, '\n')); err != nil {
		return res, err
	}
	return res, register(Project{Path: dir, Port: port})
}

func ignore(path, line string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return os.WriteFile(path, append(b, []byte(line+"\n")...), 0o644)
}
