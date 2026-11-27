package improve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/devex/harness/internal/events"
)

func ev(name, run string, data map[string]any) events.Event {
	if data == nil {
		data = map[string]any{}
	}
	data["run"] = run
	return events.Event{Ev: name, Data: data}
}

func start(run, task, ver string) events.Event {
	return ev("eval-start", run, map[string]any{"task": task, "harness_version": ver})
}

func end(run string, pass bool) events.Event {
	return ev("eval-end", run, map[string]any{"status": "done", "pass": pass})
}

func TestJudgeScoresAndIsDeterministic(t *testing.T) {
	log := []events.Event{
		start("r1", "a", "v1"),
		{Ev: "gate", Data: map[string]any{"result": "pass"}}, // no run: ignored
		ev("gate", "r1", map[string]any{"result": "pass"}),   // agent-recorded gate: not trusted
		ev("cost", "r1", map[string]any{"amount": 3.0}),      // over the 2.0 budget
		ev("finding", "r1", map[string]any{"severity": "High"}),
		ev("review-result", "r1", map[string]any{"annotations": 2.0}),
		ev("tool-error", "r1", nil),
		end("r1", false),
		ev("cost", "r1", map[string]any{"amount": 100.0}), // after eval-end: not part of the run
		start("r2", "a", "v1"),
		end("r2", true),
	}
	got := Judge(log, 2)
	if len(got) != 2 {
		t.Fatalf("runs: %+v", got)
	}
	r1, r2 := got[0], got[1]
	// gate 0 (eval-end says fail), security 0, cost 0, rework 10*(1-2/5)=6, errors 10*(1-1/5)=8
	if !r1.Scored || r1.Pass || r1.Score != 14 || r1.CostUSD != 3 {
		t.Fatalf("r1 %+v", r1)
	}
	if !r2.Scored || r2.Score != 100 {
		t.Fatalf("r2 %+v", r2)
	}
	for i := 0; i < 5; i++ {
		if again := Judge(log, 2); !reflect.DeepEqual(again, got) {
			t.Fatalf("judge is not deterministic:\n%+v\n%+v", got, again)
		}
	}
}

func TestBrokenEvalsAreUnscoredNotZero(t *testing.T) {
	log := []events.Event{
		start("ok", "a", "v1"), end("ok", false), // a real failure: scored, low
		start("crash", "b", "v1"), // never ended
		start("setup", "b", "v1"),
		ev("eval-end", "setup", map[string]any{"status": "error", "error": "setup failed"}),
	}
	runs := Judge(log, 0)
	if runs[0].Scored != true || runs[1].Scored || runs[2].Scored {
		t.Fatalf("scored flags: %+v", runs)
	}
	if !strings.Contains(runs[2].Reason, "setup failed") {
		t.Fatalf("reason %q", runs[2].Reason)
	}
	vs := Summarize(runs, []string{"a", "b"}, "all")
	if len(vs) != 1 {
		t.Fatalf("versions %+v", vs)
	}
	// b's unscored runs are excluded from the mean, not counted as 0.
	if vs[0].Mean != runs[0].Score || !reflect.DeepEqual(vs[0].Missing, []string{"b"}) {
		t.Fatalf("summary %+v", vs[0])
	}
}

func TestSummarizeAndCompare(t *testing.T) {
	log := []events.Event{
		start("b1", "a", "base"), end("b1", true),
		start("b2", "b", "base"), ev("tool-error", "b2", nil), end("b2", true),
		start("c1", "a", "cand"), end("c1", true),
		start("c2", "b", "cand"), end("c2", true),
		start("x", "other", "cand"), end("x", false), // not in the split
	}
	vs := Summarize(Judge(log, 0), []string{"a", "b"}, "test")
	if len(vs) != 2 || vs[0].Version != "base" || vs[1].Version != "cand" {
		t.Fatalf("versions %+v", vs)
	}
	v := Compare(vs[0], vs[1])
	if !v.Propose || v.Delta != 1 { // base mean (100+98)/2 = 99, cand 100
		t.Fatalf("verdict %+v", v)
	}
	if back := Compare(vs[1], vs[0]); back.Propose {
		t.Fatalf("a worse candidate must not be proposed: %+v", back)
	}

	// A regression blocks even with a higher mean elsewhere; a task the
	// candidate failed to score blocks too.
	log = append(log,
		start("c3", "b", "cand2"), end("c3", false),
		start("c4", "a", "cand3"), end("c4", true))
	vs = Summarize(Judge(log, 0), []string{"a", "b"}, "test")
	byVer := map[string]VersionScore{}
	for _, s := range vs {
		byVer[s.Version] = s
	}
	if v := Compare(byVer["base"], byVer["cand2"]); v.Propose || !reflect.DeepEqual(v.Regressions, []string{"b"}) {
		t.Fatalf("regression verdict %+v", v)
	}
	if v := Compare(byVer["base"], byVer["cand3"]); v.Propose {
		t.Fatalf("missing task must block: %+v", v)
	}
}

func TestCompareCost(t *testing.T) {
	base := VersionScore{Version: "b", Tasks: []TaskScore{{Task: "a", Score: 80, PassRate: 1, CostUSD: 1, Runs: 1}}}
	cand := VersionScore{Version: "c", Tasks: []TaskScore{{Task: "a", Score: 90, PassRate: 1, CostUSD: 1.2, Runs: 1}}}
	if v := Compare(base, cand); v.Propose {
		t.Fatalf("20%% cost rise must block: %+v", v)
	}
	cand.Tasks[0].CostUSD = 1.1
	if v := Compare(base, cand); !v.Propose {
		t.Fatalf("10%% cost rise is allowed: %+v", v)
	}
}

func TestSplitIsSeededAndStable(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	s1, s2 := NewSplit(ids, 42, 0.3), NewSplit(ids, 42, 0.3)
	if !reflect.DeepEqual(s1, s2) || len(s1.Test) != 3 || len(s1.Train) != 7 {
		t.Fatalf("split %+v", s1)
	}
	if reflect.DeepEqual(s1.Test, NewSplit(ids, 7, 0.3).Test) {
		t.Fatal("a different seed should (for this set) give a different test split")
	}
	if s := NewSplit([]string{"x", "y"}, 1, 0.1); len(s.Test) != 1 {
		t.Fatalf("two tasks must still give one test task: %+v", s)
	}
	if _, err := s1.Of("dev"); err == nil {
		t.Fatal("unknown split name must error")
	}
}

func TestLoadTasks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "evals", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(name string, v any) {
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("b.json", Task{ID: "b", Prompt: "do b", Problem: "pb"})
	put("a.json", Task{ID: "a", Prompt: "do a", Problem: "pa", Mode: "explain"})
	tasks, err := LoadTasks(root)
	if err != nil || len(tasks) != 2 || tasks[0].ID != "a" || tasks[1].Mode != "build" {
		t.Fatalf("%+v %v", tasks, err)
	}
	put("c.json", Task{ID: "c", Prompt: "x", Problem: "../escape"})
	if _, err := LoadTasks(root); err == nil {
		t.Fatal("path-like problem id must be rejected")
	}
}

// The repo's own eval set must load and match the practice problems.
func TestRepoEvalSet(t *testing.T) {
	root := filepath.Join("..", "..")
	tasks, err := LoadTasks(root)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("%v (%d tasks)", err, len(tasks))
	}
	for _, task := range tasks {
		if _, err := os.Stat(filepath.Join(root, "practice-problems", task.Problem+".json")); err != nil {
			t.Errorf("task %s: %v", task.ID, err)
		}
	}
}
