package harness

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/events"
)

// --- /api/apps: external tools found on this developer's machine ---
//
// A fixed catalog: the browser only ever sends an id, so it can't choose what
// gets executed. GET detects; POST launches a desktop app.

type appSpec struct {
	id, name, about string
	app             string // macOS bundle name without .app
	bin             string // command on PATH
	url             string // web UI, probed for "running"
	dir             string // folder under the harness tools dir
	hint            string // how to start it by hand
	openProject     bool   // open the app at the project root
}

var appCatalog = []appSpec{
	{id: "learnharness", name: "LearnHarness", about: "Lessons and guided practice (desktop app)", app: "LearnHarness"},
	{id: "artex", name: "ARTEX", about: "Security auditor. Only point it at systems you may test", url: "http://127.0.0.1:8787/", dir: "ARTEX", hint: "docker compose up -d"},
	{id: "vscode", name: "VS Code", about: "Editor, opens this project", app: "Visual Studio Code", openProject: true},
	{id: "cursor", name: "Cursor", about: "AI editor, opens this project", app: "Cursor", openProject: true},
	{id: "opencode", name: "OpenCode", about: "Open-source coding agent", app: "OpenCode", bin: "opencode", hint: "opencode"},
	{id: "claude", name: "Claude Code", about: "Coding agent in your terminal", bin: "claude", hint: "claude"},
	{id: "codex", name: "Codex CLI", about: "Coding agent in your terminal", bin: "codex", hint: "codex"},
	{id: "gemini", name: "Gemini CLI", about: "Coding agent in your terminal", bin: "gemini", hint: "gemini"},
	{id: "plannotator", name: "Plannotator", about: "Review and annotate plans, specs and diffs", bin: "plannotator", hint: "plannotator review"},
	{id: "graphify", name: "Graphify", about: "Builds the knowledge graph for the Graph tab", bin: "graphify", hint: "/graphify in Claude Code"},
	{id: "trail", name: "Agent Trail", about: "Browse past agent sessions", bin: "agent-trail", url: "http://127.0.0.1:3030/", hint: "HOSTNAME=127.0.0.1 agent-trail"},
	{id: "tokentelemetry", name: "TokenTelemetry", about: "Token and cost dashboard", url: "http://127.0.0.1:13000/", dir: "tokentelemetry", hint: "./start.sh"},
	{id: "ollama", name: "Ollama", about: "Runs models on your machine", app: "Ollama", bin: "ollama", url: "http://127.0.0.1:11434/", hint: "ollama serve"},
}

var (
	openCommand = "open" // overridden in tests
	appDirs     = func() []string {
		home, _ := os.UserHomeDir()
		return []string{"/Applications", filepath.Join(home, "Applications")}
	}
)

type appStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	About      string `json:"about"`
	Installed  bool   `json:"installed"`
	Running    bool   `json:"running"`
	Launchable bool   `json:"launchable"`
	URL        string `json:"url,omitempty"` // only when running
	Hint       string `json:"hint,omitempty"`
}

// appPath returns the installed bundle path, or "".
func appPath(name string) string {
	if name == "" {
		return ""
	}
	for _, d := range appDirs() {
		p := filepath.Join(d, name+".app")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func detectApp(s appSpec) appStatus {
	st := appStatus{ID: s.id, Name: s.name, About: s.about, Hint: s.hint}
	st.Launchable = appPath(s.app) != ""
	st.Installed = st.Launchable
	if s.bin != "" {
		if _, err := exec.LookPath(s.bin); err == nil {
			st.Installed = true
		}
	}
	if s.dir != "" {
		if _, err := os.Stat(filepath.Join(config.ToolsDir(), s.dir)); err == nil {
			st.Installed = true
		}
	}
	if s.url != "" && probe(s.url).available {
		st.Installed, st.Running, st.URL = true, true, s.url
	}
	return st
}

func (rt *Runtime) handleApps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		found := make([]appStatus, len(appCatalog))
		var wg sync.WaitGroup
		for i, s := range appCatalog {
			wg.Add(1)
			go func() {
				defer wg.Done()
				found[i] = detectApp(s)
			}()
		}
		wg.Wait()
		out := []appStatus{}
		for _, a := range found {
			if a.Installed {
				out = append(out, a)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"apps": out})
	case http.MethodPost:
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		for _, s := range appCatalog {
			if s.id != req.ID {
				continue
			}
			path := appPath(s.app)
			if path == "" {
				http.Error(w, s.name+" is not an installed desktop app", http.StatusBadRequest)
				return
			}
			args := []string{path}
			if s.openProject {
				args = []string{"-a", path, rt.root}
			}
			if err := exec.Command(openCommand, args...).Run(); err != nil {
				http.Error(w, "failed to launch: "+err.Error(), http.StatusInternalServerError)
				return
			}
			rt.log.Append(events.Event{Ev: "app-launch", Tool: s.id})
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "launched"})
			return
		}
		http.Error(w, "unknown app", http.StatusNotFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
