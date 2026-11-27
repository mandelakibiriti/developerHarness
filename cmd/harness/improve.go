package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	harnessctx "github.com/devex/harness/context"
	"github.com/devex/harness/internal/compiler"
	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/improve"
	"github.com/devex/harness/internal/practice"
)

// cmdImprove: the eval/judge half of the self-improvement loop.
func cmdImprove(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "split":
		improveSplit(args[1:])
	case "check":
		improveCheck(args[1:])
	case "begin":
		improveBegin(args[1:])
	case "end":
		improveEnd(args[1:])
	case "judge":
		improveJudge(args[1:])
	case "report":
		improveReport(args[1:])
	case "propose":
		improvePropose(args[1:])
	case "apply":
		improveApply(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

func loadRoot(configPath string) (*config.Config, string) {
	cfg, err := config.Load(configPath)
	fatal(err)
	return cfg, resolveRoot(cfg, configPath)
}

func readLog(root string) []events.Event {
	evs, err := events.ReadAll(filepath.Join(root, ".harness", "events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	fatal(err)
	return evs
}

func improveSplit(args []string) {
	fs := flag.NewFlagSet("improve split", flag.ExitOnError)
	seed := fs.Int64("seed", 42, "split seed")
	testFrac := fs.Float64("test", 0.3, "fraction of tasks held out for test")
	force := fs.Bool("force", false, "overwrite an existing split (earlier scores stop being comparable)")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)
	if *testFrac <= 0 || *testFrac >= 1 {
		fatal(fmt.Errorf("--test must be between 0 and 1"))
	}
	_, root := loadRoot(*configPath)
	if _, err := os.Stat(improve.SplitPath(root)); err == nil && !*force {
		fatal(fmt.Errorf("%s exists; pass --force to re-split", improve.SplitPath(root)))
	}
	tasks, err := improve.LoadTasks(root)
	fatal(err)
	if len(tasks) == 0 {
		fatal(fmt.Errorf("no tasks in evals/tasks"))
	}
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	s := improve.NewSplit(ids, *seed, *testFrac)
	b, _ := json.MarshalIndent(s, "", "  ")
	fatal(os.WriteFile(improve.SplitPath(root), append(b, '\n'), 0o644))
	fmt.Printf("split seed %d: train %v, test %v\n", s.Seed, s.Train, s.Test)
}

// improveCheck is the harness2-style spec gate: structural validity only, no
// model calls and no writes. Run it before a probe so a broken candidate is
// rejected cheaply instead of producing an unscored eval run.
func improveCheck(args []string) {
	fs := flag.NewFlagSet("improve check", flag.ExitOnError)
	mode := fs.String("mode", "build", "compiler mode")
	level := fs.String("level", "", "level (default: harness.json level)")
	domain := fs.String("domain", "", "lessons domain")
	skipTests := fs.Bool("skip-tests", false, "skip go vet and go test -race")
	asJSON := fs.Bool("json", false, "print JSON")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)

	cfg, root := loadRoot(*configPath)
	if *level == "" {
		*level = cfg.Level
	}
	items := improve.Check(context.Background(), root, cfg, improve.CheckOptions{
		Mode: *mode, Level: *level, Domain: *domain, SkipTests: *skipTests,
	})
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		fatal(enc.Encode(items))
		return
	}
	failures := 0
	for _, it := range items {
		mark := "ok   "
		if !it.OK {
			mark = "FAIL "
			failures++
		}
		line := fmt.Sprintf("[%s] %s — %s", mark, it.Name, it.Detail)
		if !it.OK && it.Fix != "" {
			line += "  → " + it.Fix
		}
		fmt.Println(line)
	}
	fmt.Printf("\n%d checks, %d failed\n", len(items), failures)
	if failures > 0 {
		os.Exit(1)
	}
}

// improveBegin logs eval-start and prints the run id for HARNESS_RUN.
func improveBegin(args []string) {
	fs := flag.NewFlagSet("improve begin", flag.ExitOnError)
	taskID := fs.String("task", "", "eval task id")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)
	cfg, root := loadRoot(*configPath)
	task := findTask(root, *taskID)
	level := task.Level
	if level == "" {
		level = cfg.Level
	}
	c := &compiler.Compiler{Root: root, Cfg: cfg, Defaults: harnessctx.FS}
	b, err := c.Compile(compiler.Request{Mode: task.Mode, Level: level, Domain: task.Domain})
	fatal(err)
	run := task.ID + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	appendRuntimeEvent(cfg, *configPath, events.Event{Ev: "eval-start", Data: map[string]any{
		"run": run, "task": task.ID, "harness_version": b.Version, "level": level}})
	fmt.Println(run)
}

func findTask(root, id string) improve.Task {
	tasks, err := improve.LoadTasks(root)
	fatal(err)
	for _, t := range tasks {
		if t.ID == id {
			return t
		}
	}
	fatal(fmt.Errorf("no eval task %q in evals/tasks", id))
	return improve.Task{}
}

// improveEnd runs the task's test itself (the only pass the judge trusts) and
// logs eval-end. A test that cannot run marks the run as an eval error.
func improveEnd(args []string) {
	fs := flag.NewFlagSet("improve end", flag.ExitOnError)
	run := fs.String("run", "", "run id from `harness improve begin`")
	workdir := fs.String("root", "", "tree the agent worked in (default: project root)")
	broken := fs.String("error", "", "record the run as an eval error with this reason, without testing")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)
	cfg, root := loadRoot(*configPath)

	taskID := ""
	for _, ev := range readLog(root) {
		if r, _ := ev.Data["run"].(string); r != *run || *run == "" {
			continue
		}
		switch ev.Ev {
		case "eval-start":
			taskID, _ = ev.Data["task"].(string)
		case "eval-end":
			fatal(fmt.Errorf("run %s already ended", *run))
		}
	}
	if taskID == "" {
		fatal(fmt.Errorf("no eval-start for run %q", *run))
	}
	data := map[string]any{"run": *run, "status": "done"}
	if *broken != "" {
		data["status"], data["error"] = "error", *broken
	} else {
		dir := root
		if *workdir != "" {
			dir = *workdir
		}
		p, err := practice.Load(filepath.Join(dir, "practice-problems", findTask(root, taskID).Problem+".json"))
		fatal(err)
		if !filepath.IsAbs(p.Cwd) {
			p.Cwd = filepath.Join(dir, p.Cwd)
		}
		res := p.Run()
		data["pass"], data["exit_code"] = res.Pass, res.ExitCode
		if res.Error != "" { // setup or launch failure: the eval broke, not the task
			data["status"], data["error"] = "error", res.Error
		}
	}
	appendRuntimeEvent(cfg, *configPath, events.Event{Ev: "eval-end", Data: data})
	fmt.Printf("run %s: status %s, pass %v\n", *run, data["status"], data["pass"])
}

func judged(root, split string, cfg *config.Config) []improve.VersionScore {
	var ids []string
	if split == "all" {
		tasks, err := improve.LoadTasks(root)
		fatal(err)
		for _, t := range tasks {
			ids = append(ids, t.ID)
		}
	} else {
		s, err := improve.LoadSplit(root)
		fatal(err)
		ids, err = s.Of(split)
		fatal(err)
	}
	return improve.Summarize(improve.Judge(readLog(root), cfg.Budgets.PerFeatureUSD), ids, split)
}

// improveJudge scores every version seen in the log and archives each result
// under runs/<version>/judged.<split>.json.
func improveJudge(args []string) {
	fs := flag.NewFlagSet("improve judge", flag.ExitOnError)
	split := fs.String("split", "all", "train|test|all")
	asJSON := fs.Bool("json", false, "print JSON")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)
	cfg, root := loadRoot(*configPath)
	all := judged(root, *split, cfg)
	for _, vs := range all {
		dir := filepath.Join(root, "runs", vs.Version)
		fatal(os.MkdirAll(dir, 0o755))
		b, _ := json.MarshalIndent(vs, "", "  ")
		fatal(os.WriteFile(filepath.Join(dir, "judged."+*split+".json"), append(b, '\n'), 0o644))
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		fatal(enc.Encode(all))
		return
	}
	if len(all) == 0 {
		fmt.Println("no eval runs in the log yet (see evals/README.md)")
	}
	for _, vs := range all {
		fmt.Printf("%s  %s  mean %.2f  cost/task $%.4f  missing %v\n", vs.Version, vs.Split, vs.Mean, vs.CostUSD, vs.Missing)
		for _, t := range vs.Tasks {
			fmt.Printf("  %-28s %6.2f  pass %.0f%%  runs %d  unscored %d\n", t.Task, t.Score, t.PassRate*100, t.Runs, t.Unscored)
		}
	}
}

// improveReport applies the adoption rule; exit 1 when cand is not proposable.
func improveReport(args []string) {
	fs := flag.NewFlagSet("improve report", flag.ExitOnError)
	base := fs.String("base", "", "base harness version")
	cand := fs.String("cand", "", "candidate harness version")
	split := fs.String("split", "test", "train|test|all")
	asJSON := fs.Bool("json", false, "print JSON")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)
	cfg, root := loadRoot(*configPath)
	var b, c *improve.VersionScore
	all := judged(root, *split, cfg)
	for i := range all {
		switch all[i].Version {
		case *base:
			b = &all[i]
		case *cand:
			c = &all[i]
		}
	}
	if b == nil || c == nil {
		fatal(fmt.Errorf("both --base and --cand need eval runs on the %s split", *split))
	}
	v := improve.Compare(*b, *c)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		fatal(enc.Encode(v))
	} else {
		fmt.Printf("%s → %s on %s (%d tasks): %.2f → %.2f (%+.2f), cost/task $%.4f → $%.4f\n",
			v.Base, v.Cand, *split, len(v.Compared), v.BaseMean, v.CandMean, v.Delta, v.BaseCost, v.CandCost)
		verdict := "not proposable"
		if v.Propose {
			verdict = "proposable (still needs approval in Plannotator)"
		}
		fmt.Printf("%s: %s\n", verdict, strings.Join(v.Reasons, "; "))
	}
	if !v.Propose {
		os.Exit(1)
	}
}

// improvePropose turns the scored log into candidates: it compiles the current
// version (the base), judges the runs, resolves the configured proposer, and
// archives its candidates under runs/<base>/proposals.json. Nothing is written
// to the learnable files here — that is improveApply's job.
func improvePropose(args []string) {
	fs := flag.NewFlagSet("improve propose", flag.ExitOnError)
	base := fs.String("base", "", "base harness version (default: current compiled version)")
	k := fs.Int("k", 0, "max candidates (default: harness.json improve.k)")
	asJSON := fs.Bool("json", false, "print proposals as JSON")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)
	cfg, root := loadRoot(*configPath)

	baseVersion := *base
	if baseVersion == "" {
		c := &compiler.Compiler{Root: root, Cfg: cfg, Defaults: harnessctx.FS}
		b, err := c.Compile(compiler.Request{Mode: "build", Level: cfg.Level})
		fatal(err)
		baseVersion = b.Version
	}
	kCount := *k
	if kCount <= 0 {
		kCount = cfg.Improve.K
	}
	evs := readLog(root)
	runs := improve.Judge(evs, cfg.Budgets.PerFeatureUSD)
	evidence := make([]improve.Evidence, 0, len(runs))
	for _, r := range runs {
		if r.Scored {
			evidence = append(evidence, improve.Evidence{Run: r})
		}
	}
	p, err := improve.ResolveProposer(cfg, root)
	fatal(err)
	cands, err := p.Propose(context.Background(), improve.ProposeRequest{
		Root: root, Base: baseVersion, Evidence: evidence, K: kCount,
	})
	fatal(err)

	dir := filepath.Join(root, "runs", baseVersion)
	fatal(os.MkdirAll(dir, 0o755))
	b, _ := json.MarshalIndent(cands, "", "  ")
	fatal(os.WriteFile(filepath.Join(dir, "proposals.json"), append(b, '\n'), 0o644))

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		fatal(enc.Encode(cands))
		return
	}
	fmt.Printf("proposed %d candidates for %s → %s\n", len(cands), baseVersion, filepath.Join("runs", baseVersion, "proposals.json"))
}

// improveApply writes the proposals recorded for a base version, after each
// candidate passes ValidateCandidate. It reports which candidates were rejected
// and exits non-zero if any failed to apply.
func improveApply(args []string) {
	fs := flag.NewFlagSet("improve apply", flag.ExitOnError)
	base := fs.String("base", "", "base harness version whose proposals to apply")
	cand := fs.String("cand", "", "apply only this candidate id")
	all := fs.Bool("all", false, "apply every candidate in the proposals file")
	configPath := fs.String("config", "harness.json", "path to harness.json")
	fs.Parse(args)
	if *base == "" {
		fatal(fmt.Errorf("--base is required"))
	}
	cfg, root := loadRoot(*configPath)

	proposalsPath := filepath.Join(root, "runs", *base, "proposals.json")
	b, err := os.ReadFile(proposalsPath)
	if err != nil {
		fatal(fmt.Errorf("read %s: %w", proposalsPath, err))
	}
	var candidates []improve.Candidate
	if err := json.Unmarshal(b, &candidates); err != nil {
		fatal(fmt.Errorf("parse %s: %w", proposalsPath, err))
	}
	selected := candidates
	if !*all && *cand != "" {
		selected = nil
		for _, c := range candidates {
			if c.ID == *cand {
				selected = append(selected, c)
			}
		}
	}
	rejected := improve.ApplyAll(root, cfg, selected)
	fmt.Printf("applied %d, rejected %d\n", len(selected)-len(rejected), len(rejected))
	for _, c := range rejected {
		fmt.Printf("  rejected %s (%s)\n", c.ID, c.File)
	}
	if len(rejected) > 0 {
		os.Exit(1)
	}
}
