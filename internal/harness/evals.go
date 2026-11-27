package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/devex/harness/internal/compiler"
	"github.com/devex/harness/internal/events"
	"github.com/devex/harness/internal/improve"
	"github.com/devex/harness/internal/practice"
)

// --- /api/evals: read-only view of the self-improvement loop (`harness improve`) ---
//
// Runs are executed from the CLI; this only reports what the event log, evals/
// and runs/ already record. Scores come from the same judge the CLI uses.

type evalRun struct {
	improve.RunScore
	Started string `json:"started"`
	Split   string `json:"split"`
	Outcome string `json:"outcome"` // pass | fail | unscored | open
}

type evalTask struct {
	ID    string `json:"id"`
	Split string `json:"split"`
	Test  string `json:"test"` // the command that decides pass/fail
	Runs  int    `json:"runs"`
}

type evalProposal struct {
	Base string `json:"base"`
	ID   string `json:"id"`
	File string `json:"file"`
	Why  string `json:"why"`
}

// signal is something that went wrong in everyday use: raw material for a new
// rule, lesson or eval task.
type signal struct {
	T      string `json:"t"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type evalReport struct {
	Current   string                 `json:"current_version"`
	Tasks     []evalTask             `json:"tasks"`
	Runs      []evalRun              `json:"runs"` // newest first
	Versions  []improve.VersionScore `json:"versions"`
	Verdict   *improve.Verdict       `json:"verdict,omitempty"` // last two versions, test split
	Proposals []evalProposal         `json:"proposals"`
	Signals   []signal               `json:"signals"` // newest first
	Counts    map[string]int         `json:"signal_counts"`
	Problems  []string               `json:"problems"` // why parts of the report are missing
}

const recentSignals = 15

func everydaySignal(ev events.Event) (kind, detail string) {
	d := ev.Data
	if str(d["run"]) != "" { // part of an eval run, not everyday use
		return "", ""
	}
	switch ev.Ev {
	case "llm-error":
		if c, _ := d["cancelled"].(bool); c {
			return "stopped by developer", str(d["purpose"])
		}
		return "model error", str(d["purpose"]) + ": " + str(d["error"])
	case "gate":
		if strings.EqualFold(str(d["result"]), "fail") {
			return "failed gate", str(d["name"])
		}
	case "run":
		if code := num(d["exit"]); code != 0 {
			return "failed code run", fmt.Sprintf("%s exited %d", str(d["lang"]), code)
		}
	case "review-finished":
		if s := str(d["decision"]); s != "" && s != "approved" {
			return "review asked for changes", s
		}
	case "tool-error":
		return "tool error", ev.Tool + ": " + str(d["error"])
	case "finding":
		return "security finding", str(d["severity"])
	}
	return "", ""
}

func (rt *Runtime) evalReport() (evalReport, error) {
	rep := evalReport{Tasks: []evalTask{}, Runs: []evalRun{}, Versions: []improve.VersionScore{},
		Proposals: []evalProposal{}, Signals: []signal{}, Counts: map[string]int{}, Problems: []string{}}
	evs, err := events.ReadAll(rt.log.Path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return rep, err
	}
	if b, err := rt.compile(compiler.Request{Mode: "build"}); err == nil {
		rep.Current = b.Version
	} else {
		rep.Problems = append(rep.Problems, "context does not compile: "+err.Error())
	}

	tasks, err := improve.LoadTasks(rt.root)
	if err != nil {
		rep.Problems = append(rep.Problems, err.Error())
	}
	splitOf := map[string]string{}
	split, err := improve.LoadSplit(rt.root)
	if err != nil && len(tasks) > 0 {
		rep.Problems = append(rep.Problems, err.Error())
	}
	for _, id := range split.Train {
		splitOf[id] = "train"
	}
	for _, id := range split.Test {
		splitOf[id] = "test"
	}

	started := map[string]string{}
	for _, ev := range evs {
		if ev.Ev == "eval-start" {
			started[str(ev.Data["run"])] = ev.T
		}
		if kind, detail := everydaySignal(ev); kind != "" {
			rep.Counts[kind]++
			rep.Signals = append(rep.Signals, signal{T: ev.T, Kind: kind, Detail: detail})
		}
	}
	for i, j := 0, len(rep.Signals)-1; i < j; i, j = i+1, j-1 {
		rep.Signals[i], rep.Signals[j] = rep.Signals[j], rep.Signals[i]
	}
	if len(rep.Signals) > recentSignals {
		rep.Signals = rep.Signals[:recentSignals]
	}

	scores := improve.Judge(evs, rt.cfg.Budgets.PerFeatureUSD)
	perTask := map[string]int{}
	var order []string // versions in the order they were first evaluated
	seen := map[string]bool{}
	for _, s := range scores {
		outcome := "fail"
		switch {
		case !s.Scored && strings.HasPrefix(s.Reason, "no eval-end"):
			outcome = "open"
		case !s.Scored:
			outcome = "unscored"
		case s.Pass:
			outcome = "pass"
		}
		perTask[s.Task]++
		if s.Version != "" && !seen[s.Version] {
			seen[s.Version] = true
			order = append(order, s.Version)
		}
		rep.Runs = append([]evalRun{{RunScore: s, Started: started[s.Run], Split: splitOf[s.Task], Outcome: outcome}}, rep.Runs...)
	}

	var ids []string
	for _, t := range tasks {
		ids = append(ids, t.ID)
		test := "problem file missing"
		if p, err := practice.Load(filepath.Join(rt.root, "practice-problems", t.Problem+".json")); err == nil {
			test = strings.Join(p.Process, " ")
		}
		rep.Tasks = append(rep.Tasks, evalTask{ID: t.ID, Split: splitOf[t.ID], Test: test, Runs: perTask[t.ID]})
	}
	byVersion := map[string]improve.VersionScore{}
	for _, v := range improve.Summarize(scores, ids, "all") {
		byVersion[v.Version] = v
	}
	for _, v := range order {
		if vs, ok := byVersion[v]; ok {
			rep.Versions = append(rep.Versions, vs)
		}
	}
	if len(order) >= 2 && len(split.Test) > 0 {
		test := map[string]improve.VersionScore{}
		for _, v := range improve.Summarize(scores, split.Test, "test") {
			test[v.Version] = v
		}
		v := improve.Compare(test[order[len(order)-2]], test[order[len(order)-1]])
		rep.Verdict = &v
	}

	files, _ := filepath.Glob(filepath.Join(rt.root, "runs", "*", "proposals.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		var cands []improve.Candidate
		if err == nil {
			err = json.Unmarshal(b, &cands)
		}
		if err != nil {
			rep.Problems = append(rep.Problems, f+": "+err.Error())
			continue
		}
		for _, c := range cands {
			rep.Proposals = append(rep.Proposals, evalProposal{Base: filepath.Base(filepath.Dir(f)), ID: c.ID, File: c.File, Why: c.Why})
		}
	}
	return rep, nil
}

func (rt *Runtime) handleEvals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rep, err := rt.evalReport()
	if err != nil {
		http.Error(w, "could not read event log: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rep)
}
