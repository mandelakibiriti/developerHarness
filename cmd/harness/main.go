// Command harness is the launcher: harness up|down|status|doctor|level, plus
// gate|cost|scope|practice|promote for the cycle's evidence and guardrails.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	harnessctx "github.com/devex/harness/context"
	"github.com/devex/harness/internal/codemode"
	"github.com/devex/harness/internal/compiler"
	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/doctor"
	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/harness"
	"github.com/devex/harness/internal/mcp"
	"github.com/devex/harness/internal/practice"
	"github.com/devex/harness/internal/proc"
	"github.com/devex/harness/internal/promote"
	"github.com/devex/harness/internal/scaffold"
	"github.com/devex/harness/internal/scope"
	"github.com/devex/harness/internal/track"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "up":
		cmdUp(os.Args[2:])
	case "down":
		cmdDown()
	case "status":
		cmdStatus()
	case "doctor":
		cmdDoctor(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	case "level":
		cmdLevel(os.Args[2:])
	case "gate":
		cmdGate(os.Args[2:])
	case "cost":
		cmdCost(os.Args[2:])
	case "scope":
		cmdScope(os.Args[2:])
	case "practice":
		cmdPractice(os.Args[2:])
	case "promote":
		cmdPromote(os.Args[2:])
	case "context":
		cmdContext(os.Args[2:])
	case "mcp":
		cmdMCP(os.Args[2:])
	case "improve":
		cmdImprove(os.Args[2:])
	case "graph":
		cmdGraph(os.Args[2:])
	case "projects":
		cmdProjects()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `DevEx Harness — one web view over the teaching/review/mapping/cost/security tools.

Usage:
  harness init     [folder] [--port N]   (set up a project; run once per project)
  harness projects                       (list initialised projects and which are running)
  harness up       [--profile core|observe|security] [--level beginner|intermediate|advanced] [--goal "text"] [--config path]
  harness down
  harness status
  harness doctor   [--profile core|observe|security] [--config path]
  harness level    <beginner|intermediate|advanced>
  harness gate     --name <name> --result pass|fail [--output path] [--hint-rung N]
  harness cost     --amount <usd> [--currency USD] [--feature name]
  harness scope    [--confirm]
  harness practice <problem.json>
  harness promote  [--apply]
  harness mcp      [--config path]   (stdio MCP server: Code Mode search/describe/execute)
  harness improve  split [--seed N] [--test 0.3] [--force]
  harness improve  check [--mode M] [--level L] [--skip-tests] [--json]
  harness improve  propose [--base V] [--k N] [--json]
  harness improve  apply --base V [--all | --cand ID]
  harness improve  begin --task ID            (prints the run id; export it as HARNESS_RUN)
  harness improve  end --run ID [--root dir] [--error reason]
  harness improve  judge [--split train|test|all] [--json]
  harness improve  report --base V --cand V [--split test] [--json]
  harness graph    embed | search [--mode lexical|semantic|hybrid] [--k N] <query> | recall [--queries file] [--k N]
  harness context  compile [--mode architect|build|explain] [--level L] [--domain D] [--out file] [--json]
`)
}

func cmdUp(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	profile := fs.String("profile", "", "profile: core|observe|security")
	level := fs.String("level", "", "level: beginner|intermediate|advanced")
	goal := fs.String("goal", "", "goal to record as a goal event")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	fatal(err)

	// Fail fast if the listen address is already bound (catches a stale harness
	// whose pid file was removed).
	if ln, lerr := net.Listen("tcp", cfg.Listen); lerr != nil {
		fatal(fmt.Errorf("listen %s: %v — is another harness already running?", cfg.Listen, lerr))
	} else {
		ln.Close()
	}

	effLevel := cfg.Level
	if *level != "" {
		effLevel = *level
	}
	tr, err := track.Load(filepath.Join(filepath.Dir(*configPath), "tracks", effLevel+".json"))
	fatal(err)

	prof := *profile
	if prof == "" {
		prof = tr.Profile
	}

	root := resolveRoot(cfg, *configPath)
	runtimeDir := filepath.Join(root, ".harness")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		fatal(err)
	}

	pidPath := filepath.Join(runtimeDir, "harness.pid")
	if b, err := os.ReadFile(pidPath); err == nil {
		if pid, e := strconv.Atoi(strings.TrimSpace(string(b))); e == nil && processAlive(pid) {
			fatal(fmt.Errorf("harness already running (pid %d); run `harness down` first", pid))
		}
	}

	log, err := events.Open(filepath.Join(runtimeDir, "events.jsonl"))
	fatal(err)
	defer log.Close()

	rt := harness.New(cfg, prof, runtimeDir, log)
	rt.SetTrack(tr)
	rt.SetProject(root, *configPath)
	if err := rt.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
	}
	log.Append(events.Event{Ev: "harness-start", Data: map[string]any{"profile": prof, "level": effLevel, "listen": cfg.Listen}})
	if *goal != "" {
		log.Append(events.Event{Ev: "goal", Data: map[string]any{"goal": *goal}})
	}

	srv := &http.Server{Addr: cfg.Listen, Handler: rt.Handler()}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "serve:", err)
		}
	}()

	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		fatal(err)
	}

	fmt.Printf("harness up — %s profile\n", prof)
	fmt.Printf("level %s — agent mode: %s, gates: %s\n", tr.Level, tr.AgentMode, strings.Join(tr.Gates, ", "))
	if s := rt.Scope(); s != "" {
		fmt.Printf("scope: %s\n", s)
	}
	fmt.Printf("project %s\nopen http://%s\n", root, cfg.Listen)

	ctx, cancel := context.WithCancel(context.Background())
	rt.Run(ctx)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\nshutting down…")
	cancel()
	rt.Stop()
	log.Append(events.Event{Ev: "harness-stop"})
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = srv.Shutdown(shutdownCtx)
	shutdownCancel()
	_ = os.Remove(pidPath)
}

func cmdDown() {
	cfg, err := config.Load("harness.json")
	fatal(err)
	pidPath := filepath.Join(resolveRoot(cfg, "harness.json"), ".harness", "harness.pid")
	b, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("not running (no pid file)")
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	fatal(err)
	if !processAlive(pid) {
		fmt.Println("not running (stale pid file removed)")
		_ = os.Remove(pidPath)
		return
	}
	_ = proc.Signal(pid)
	for i := 0; i < 50; i++ {
		// The harness removes its pid file as its final shutdown step, so its
		// absence is authoritative (a zombie still passes kill(pid, 0)).
		if _, err := os.Stat(pidPath); os.IsNotExist(err) {
			fmt.Println("stopped")
			return
		}
		if !processAlive(pid) {
			fmt.Println("stopped")
			_ = os.Remove(pidPath)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("still running; sending SIGKILL")
	_ = proc.ForceKill(pid)
	_ = os.Remove(pidPath)
}

func cmdStatus() {
	cfg, err := config.Load("harness.json")
	fatal(err)
	resp, err := http.Get("http://" + cfg.Listen + "/api/status")
	if err != nil {
		fmt.Println("harness is not running")
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var v any
	_ = json.Unmarshal(b, &v)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func cmdDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	profile := fs.String("profile", "", "profile to check")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	fatal(err)
	prof := cfg.Profile
	if *profile != "" {
		prof = *profile
	}
	runDoctor(cfg, prof)
}

func runDoctor(cfg *config.Config, profile string) {
	findings := doctor.Run(cfg, profile)
	failures := 0
	for _, f := range findings {
		mark := "ok   "
		if !f.OK {
			mark = "FAIL "
			failures++
		}
		line := fmt.Sprintf("[%s] %s — %s", mark, f.Check, f.Detail)
		if !f.OK && f.Fix != "" {
			line += "  → " + f.Fix
		}
		fmt.Println(line)
	}
	fmt.Printf("\n%d checks, %d failed\n", len(findings), failures)
	if failures > 0 {
		os.Exit(1)
	}
}

// cmdInit sets up a new project (no harness.json yet); inside the harness's own
// repository, which has one, it installs the upstream tools as before.
func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	port := fs.Int("port", 0, "port for this project's harness (default: first free from 4400)")
	fs.Parse(args)
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
		fs.Parse(fs.Args()[1:]) // allow flags after the folder
	}
	if _, err := os.Stat(filepath.Join(dir, "harness.json")); os.IsNotExist(err) {
		res, err := scaffold.Init(dir, *port)
		fatal(err)
		fmt.Printf("initialised %s: %d files written\n", res.Dir, len(res.Written))
		for _, k := range res.Kept {
			fmt.Printf("  kept your existing %s\n", k)
		}
		if slices.Contains(res.Kept, ".claude/settings.json") {
			fmt.Println("  note: add the harness hooks to it for live agent activity")
		}
		fmt.Printf("next: cd %s && harness up    (http://127.0.0.1:%d)\n", res.Dir, res.Port)
		return
	}
	fatal(os.Chdir(dir))
	script := filepath.Join("scripts", "install-tools.sh")
	if _, err := os.Stat(script); err == nil {
		fmt.Println("installing tools (scripts/install-tools.sh)…")
		cmd := exec.Command("bash", script)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "installer failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("no scripts/install-tools.sh found — skipping install.")
	}
	cfg, err := config.Load("harness.json")
	fatal(err)
	runDoctor(cfg, cfg.Profile)
}

func cmdProjects() {
	list, err := scaffold.Projects()
	fatal(err)
	if len(list) == 0 {
		fmt.Println("no projects yet: run `harness init` inside one")
		return
	}
	for _, p := range list {
		state := "stopped"
		b, err := os.ReadFile(filepath.Join(p.Path, ".harness", "harness.pid"))
		if err == nil {
			if pid, e := strconv.Atoi(strings.TrimSpace(string(b))); e == nil && processAlive(pid) {
				state = "running"
			}
		}
		fmt.Printf("%-8s http://127.0.0.1:%d  %s\n", state, p.Port, p.Path)
	}
}

func cmdLevel(args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("usage: harness level <beginner|intermediate|advanced>"))
	}
	lvl := args[0]
	if err := applyLevel(lvl, "harness.json"); err != nil {
		fatal(err)
	}
}

func applyLevel(lvl, configPath string) error {
	tr, err := track.Load(filepath.Join(filepath.Dir(configPath), "tracks", lvl+".json"))
	if err != nil {
		return err
	}
	if _, err := config.Load(configPath); err != nil {
		return err
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	m["level"] = lvl
	m["profile"] = tr.Profile
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("level → %s (profile %s, agent mode %s). Restart with `harness up` to apply.\n", lvl, tr.Profile, tr.AgentMode)
	return nil
}

func cmdGate(args []string) {
	fs := flag.NewFlagSet("gate", flag.ExitOnError)
	name := fs.String("name", "", "gate name (e.g. go-test, api-design)")
	result := fs.String("result", "", "pass or fail")
	output := fs.String("output", "", "path to gate output")
	hintRung := fs.Float64("hint-rung", 0, "highest hint-ladder rung used")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)

	if *name == "" {
		fatal(fmt.Errorf("--name is required"))
	}
	r := strings.ToLower(*result)
	if r != "pass" && r != "fail" {
		fatal(fmt.Errorf("--result must be pass or fail"))
	}
	cfg, err := config.Load(*configPath)
	fatal(err)
	ev := events.Event{Ev: "gate", Data: map[string]any{"name": *name, "result": r}}
	if *output != "" {
		ev.Data["output"] = *output
	}
	if *hintRung > 0 {
		ev.Data["hint_rung"] = *hintRung
	}
	appendRuntimeEvent(cfg, *configPath, ev)
	fmt.Printf("recorded gate %s: %s\n", *name, r)
}

func cmdCost(args []string) {
	fs := flag.NewFlagSet("cost", flag.ExitOnError)
	amount := fs.Float64("amount", 0, "cost in USD")
	currency := fs.String("currency", "USD", "currency")
	feature := fs.String("feature", "", "feature name")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)

	if *amount <= 0 {
		fatal(fmt.Errorf("--amount must be positive"))
	}
	cfg, err := config.Load(*configPath)
	fatal(err)
	ev := events.Event{Ev: "cost", Tool: "spend", Data: map[string]any{"amount": *amount, "currency": *currency}}
	if *feature != "" {
		ev.Data["feature"] = *feature
	}
	appendRuntimeEvent(cfg, *configPath, ev)
	fmt.Printf("recorded cost %.2f %s\n", *amount, *currency)
}

func cmdScope(args []string) {
	fs := flag.NewFlagSet("scope", flag.ExitOnError)
	confirm := fs.Bool("confirm", false, "write the confirmation file after review")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	fatal(err)
	var sec *config.Tool
	for i := range cfg.Tools {
		if cfg.Tools[i].Scope != nil {
			sec = &cfg.Tools[i]
			break
		}
	}
	if sec == nil {
		fmt.Println("no scoped (offensive) tool in harness.json")
		return
	}
	desc, err := scope.Validate(sec.Scope.Allow, sec.Scope.Ports, sec.Scope.Deny)
	if err != nil {
		fatal(fmt.Errorf("scope is unsafe: %v", err))
	}
	fmt.Printf("ARTEX scope (%s): %s\n", sec.ID, desc)

	runtimeDir := filepath.Join(resolveRoot(cfg, *configPath), ".harness")
	confirmedPath := filepath.Join(runtimeDir, "scope.confirmed")
	if *confirm {
		if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(confirmedPath, []byte(desc+"\n"), 0o644); err != nil {
			fatal(err)
		}
		fmt.Println("scope confirmed and recorded.")
		return
	}
	if _, err := os.Stat(confirmedPath); err == nil {
		fmt.Println("scope already confirmed.")
	} else {
		fmt.Println("not confirmed. Review the scope, then run `harness scope --confirm`.")
	}
}

func cmdPractice(args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("usage: harness practice <problem.json>"))
	}
	path := args[0]
	p, err := practice.Load(path)
	fatal(err)
	fmt.Printf("practice %s (%s): %s\n", p.ID, p.Skill, p.Goal)
	res := p.Run()
	if res.Error != "" {
		fmt.Printf("error: %s\n", res.Error)
	}
	if res.Output != "" {
		fmt.Println(res.Output)
	}
	status := "fail"
	if res.Pass {
		status = "pass"
	}
	fmt.Printf("result: %s (exit %d)\n", status, res.ExitCode)

	if cfg, err := config.Load("harness.json"); err == nil {
		ev := events.Event{Ev: "practice", Data: map[string]any{"id": p.ID, "skill": p.Skill, "result": status, "exit_code": res.ExitCode}}
		if res.Error != "" {
			ev.Data["error"] = res.Error
		}
		appendRuntimeEvent(cfg, "harness.json", ev)
	}
}

func cmdPromote(args []string) {
	fs := flag.NewFlagSet("promote", flag.ExitOnError)
	apply := fs.Bool("apply", false, "apply the promotion by updating harness.json")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	fatal(err)
	evs, err := events.ReadAll(filepath.Join(resolveRoot(cfg, *configPath), ".harness", "events.jsonl"))
	if err != nil {
		evs = nil // no log yet -> nothing to evaluate
	}

	next, ready, reason := promote.Evaluate(cfg.Level, evs)
	fmt.Printf("level %s → %s: %s\n", cfg.Level, next, reason)
	if !ready || next == cfg.Level {
		return
	}
	if *apply {
		if err := applyLevel(next, *configPath); err != nil {
			fatal(err)
		}
		return
	}
	fmt.Printf("run `harness promote --apply` to promote.\n")
}

func appendRuntimeEvent(cfg *config.Config, configPath string, ev events.Event) {
	runtimeDir := filepath.Join(resolveRoot(cfg, configPath), ".harness")
	log, err := events.Open(filepath.Join(runtimeDir, "events.jsonl"))
	fatal(err)
	defer log.Close()
	if run := os.Getenv("HARNESS_RUN"); run != "" { // attribute gate/cost/practice to the eval run
		if ev.Data == nil {
			ev.Data = map[string]any{}
		}
		if _, set := ev.Data["run"]; !set {
			ev.Data["run"] = run
		}
	}
	fatal(log.Append(ev))
}

func resolveRoot(cfg *config.Config, configPath string) string {
	root := cfg.Project.Root
	if root == "" {
		root = "."
	}
	var abs string
	if filepath.IsAbs(root) {
		abs = root
	} else {
		abs = filepath.Join(filepath.Dir(configPath), root)
	}
	if a, err := filepath.Abs(abs); err == nil {
		return a
	}
	return abs
}

func processAlive(pid int) bool { return proc.Alive(pid) }

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// cmdContext prints (or writes) the compiled context. --out lets Claude Code
// opt in: import the file from CLAUDE.md with @<file>.
func cmdContext(args []string) {
	if len(args) == 0 || args[0] != "compile" {
		usage()
		os.Exit(2)
	}
	fs := flag.NewFlagSet("context compile", flag.ExitOnError)
	mode := fs.String("mode", "", "architect|build|explain (empty = general)")
	level := fs.String("level", "", "level (default: harness.json level)")
	domain := fs.String("domain", "", "lessons domain (default: general)")
	out := fs.String("out", "", "write the system prompt to this file instead of stdout")
	asJSON := fs.Bool("json", false, "print the bundle (prefix, suffix, version, sources) as JSON")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args[1:])

	cfg, err := config.Load(*configPath)
	fatal(err)
	if *level == "" {
		*level = cfg.Level
	}
	c := &compiler.Compiler{Root: resolveRoot(cfg, *configPath), Cfg: cfg, Defaults: harnessctx.FS}
	b, err := c.Compile(compiler.Request{Mode: *mode, Level: *level, Domain: *domain})
	fatal(err)
	switch {
	case *asJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		fatal(enc.Encode(b))
	case *out != "":
		fatal(os.WriteFile(*out, []byte(b.System()+"\n"), 0o644))
		fmt.Printf("wrote %s (version %s, %d sources)\n", *out, b.Version, len(b.Sources))
	default:
		fmt.Println(b.System())
		fmt.Fprintf(os.Stderr, "version %s, %d sources\n", b.Version, len(b.Sources))
	}
}

// cmdMCP serves Code Mode to Claude Code over stdio. Register it with
// `claude mcp add harness -- harness mcp` (or the repo's .mcp.json).
// stdout carries the protocol, so diagnostics go to stderr only.
func cmdMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	fatal(err)
	root := resolveRoot(cfg, *configPath)
	cmp := &compiler.Compiler{Root: root, Cfg: cfg, Defaults: harnessctx.FS}
	guidance, err := cmp.ContextFile("tools/guidance.md")
	fatal(err)
	rt, err := codemode.New(codemode.HarnessConnectors(codemode.Env{Root: root, Cfg: cfg}), filepath.Join(root, "context", "scripts"))
	fatal(err)

	str := map[string]any{"type": "string"}
	tools := []mcp.Tool{
		{
			Name:        "search",
			Description: "Find harness Code Mode connectors by keyword. Returns [{path, description}].",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": str}},
			Call: func(_ context.Context, raw json.RawMessage) (string, error) {
				var a struct{ Query string }
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", err
				}
				b, err := json.Marshal(rt.Search(a.Query))
				return string(b), err
			},
		},
		{
			Name:        "describe",
			Description: "TypeScript declarations for a connector path (e.g. events.recent) or a whole namespace (e.g. events).",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"target": str}, "required": []string{"target"}},
			Call: func(_ context.Context, raw json.RawMessage) (string, error) {
				var a struct{ Target string }
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", err
				}
				return rt.Describe(a.Target)
			},
		},
		{
			Name:        "execute",
			Description: guidance + "\n\nNamespaces: " + strings.Join(rt.Namespaces(), ", ") + ".",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"code": str, "save_as": str, "description": str,
			}, "required": []string{"code"}},
			Call: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					Code        string `json:"code"`
					SaveAs      string `json:"save_as"`
					Description string `json:"description"`
				}
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", err
				}
				res := rt.Execute(ctx, a.Code)
				saved := ""
				if a.SaveAs != "" && res.Status == "completed" {
					if err := rt.SaveSnippet(a.SaveAs, a.Code, a.Description); err != nil {
						res.Logs = append(res.Logs, "not saved: "+err.Error())
					} else {
						saved = a.SaveAs
					}
				}
				out, err := json.Marshal(res)
				if err != nil {
					return "", err
				}
				// Evidence for the Code Mode A/B gate: what came back, not what was computed.
				appendEventQuiet(root, events.Event{Ev: "codemode", Tool: "harness-mcp", Data: map[string]any{
					"status": res.Status, "calls": res.Calls, "code_chars": len(a.Code),
					"result_chars": len(out), "truncated": res.Truncated, "saved": saved,
				}})
				if res.Status == "error" {
					return string(out), errors.New(string(out))
				}
				return string(out), nil
			},
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mcp.Serve(ctx, os.Stdin, os.Stdout, "devex-harness", "v1", tools); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		os.Exit(1)
	}
}

// appendEventQuiet records an event without ever writing to stdout (which
// belongs to the MCP protocol); a failure is reported on stderr.
func appendEventQuiet(root string, ev events.Event) {
	log, err := events.Open(filepath.Join(root, ".harness", "events.jsonl"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "event log:", err)
		return
	}
	defer log.Close()
	if err := log.Append(ev); err != nil {
		fmt.Fprintln(os.Stderr, "event log:", err)
	}
}
