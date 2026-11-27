package improve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/devex/harness/internal/events"
)

// Score weights, out of 100 points.
const (
	wGate     = 40 // the task's real test passed (eval-end)
	wSecurity = 20 // no High finding during the run
	wCost     = 20 // run cost within the per-feature budget
	wRework   = 10 // fewer Plannotator annotations = less rework
	wErrors   = 10 // fewer tool errors
	// softCap is the count at which a rework/error component reaches zero.
	softCap = 5
)

// RunScore is the judgement of one run. A run whose evaluation broke (setup
// failed, no eval-end) is unscored rather than zero, as in harness2: a broken
// eval says nothing about the harness.
type RunScore struct {
	Run     string             `json:"run"`
	Task    string             `json:"task"`
	Version string             `json:"harness_version"`
	Scored  bool               `json:"scored"`
	Reason  string             `json:"reason,omitempty"` // why unscored
	Score   float64            `json:"score"`
	Pass    bool               `json:"pass"`
	CostUSD float64            `json:"cost_usd"`
	Parts   map[string]float64 `json:"parts,omitempty"`
}

type runAcc struct {
	RunScore
	ended       bool
	status      string
	endError    string
	highs       int
	annotations float64
	toolErrors  int
}

// Judge scores every run bracketed in evs. It is a pure function of the log
// and the budget: the same events always give the same scores.
func Judge(evs []events.Event, budgetUSD float64) []RunScore {
	runs := map[string]*runAcc{}
	var order []string
	for _, ev := range evs {
		run, _ := ev.Data["run"].(string)
		if run == "" {
			continue
		}
		if ev.Ev == "eval-start" {
			if _, dup := runs[run]; !dup {
				task, _ := ev.Data["task"].(string)
				ver, _ := ev.Data["harness_version"].(string)
				runs[run] = &runAcc{RunScore: RunScore{Run: run, Task: task, Version: ver}}
				order = append(order, run)
			}
			continue
		}
		r, ok := runs[run]
		if !ok || r.ended { // before its start, or after its end: not part of the run
			continue
		}
		switch ev.Ev {
		case "eval-end":
			r.ended = true
			r.status, _ = ev.Data["status"].(string)
			r.endError, _ = ev.Data["error"].(string)
			r.Pass, _ = ev.Data["pass"].(bool)
		case "finding":
			if sev, _ := ev.Data["severity"].(string); strings.EqualFold(sev, "high") {
				r.highs++
			}
		case "cost":
			amt, _ := ev.Data["amount"].(float64)
			r.CostUSD += amt
		case "tool-error", "PostToolUseFailure":
			r.toolErrors++
		}
		if n, ok := ev.Data["annotations"].(float64); ok && n > 0 {
			r.annotations += n
		}
	}

	out := make([]RunScore, 0, len(order))
	for _, id := range order {
		out = append(out, finish(runs[id], budgetUSD))
	}
	return out
}

func finish(r *runAcc, budgetUSD float64) RunScore {
	switch {
	case !r.ended:
		r.Reason = "no eval-end (run crashed or still running)"
		return r.RunScore
	case r.status != "done":
		r.Reason = "eval error: " + r.endError
		return r.RunScore
	case r.Task == "" || r.Version == "":
		r.Reason = "eval-start lacks task or harness_version"
		return r.RunScore
	}
	r.Scored = true
	r.Parts = map[string]float64{"gate": 0, "security": 0, "cost": 0,
		"rework": wRework * soft(r.annotations), "errors": wErrors * soft(float64(r.toolErrors))}
	if r.Pass {
		r.Parts["gate"] = wGate
	}
	if r.highs == 0 {
		r.Parts["security"] = wSecurity
	}
	if budgetUSD <= 0 || r.CostUSD <= budgetUSD {
		r.Parts["cost"] = wCost
	}
	for _, k := range []string{"gate", "security", "cost", "rework", "errors"} { // fixed order: deterministic float sum
		r.Score += r.Parts[k]
	}
	return r.RunScore
}

func soft(n float64) float64 {
	if n >= softCap {
		return 0
	}
	return 1 - n/softCap
}

// TaskScore aggregates one task's scored runs under one version.
type TaskScore struct {
	Task     string  `json:"task"`
	Score    float64 `json:"score"`     // mean over scored runs
	PassRate float64 `json:"pass_rate"` // fraction of scored runs that passed
	CostUSD  float64 `json:"cost_usd"`  // mean per scored run
	Runs     int     `json:"runs"`
	Unscored int     `json:"unscored"`
}

// Pass is the task's verdict: it passes in most of its scored runs.
func (t TaskScore) Pass() bool { return t.PassRate >= 0.5 }

// VersionScore is one harness version on one split.
type VersionScore struct {
	Version string      `json:"harness_version"`
	Split   string      `json:"split"`
	Mean    float64     `json:"mean"`     // over tasks with >= 1 scored run
	CostUSD float64     `json:"cost_usd"` // mean per task
	Tasks   []TaskScore `json:"tasks"`
	Missing []string    `json:"missing,omitempty"` // split tasks with no scored run
}

// Summarize groups runs by version, keeping only tasks in ids.
func Summarize(runs []RunScore, ids []string, split string) []VersionScore {
	in := map[string]bool{}
	for _, id := range ids {
		in[id] = true
	}
	type key struct{ ver, task string }
	acc := map[key]*TaskScore{}
	vers := map[string]bool{}
	for _, r := range runs {
		if !in[r.Task] || r.Version == "" {
			continue
		}
		vers[r.Version] = true
		k := key{r.Version, r.Task}
		ts := acc[k]
		if ts == nil {
			ts = &TaskScore{Task: r.Task}
			acc[k] = ts
		}
		if !r.Scored {
			ts.Unscored++
			continue
		}
		ts.Runs++
		ts.Score += r.Score
		ts.CostUSD += r.CostUSD
		if r.Pass {
			ts.PassRate++
		}
	}

	var out []VersionScore
	for ver := range vers {
		vs := VersionScore{Version: ver, Split: split, Tasks: []TaskScore{}}
		for _, id := range sortedCopy(ids) {
			ts := acc[key{ver, id}]
			if ts == nil || ts.Runs == 0 {
				vs.Missing = append(vs.Missing, id)
				if ts == nil {
					continue
				}
			} else {
				n := float64(ts.Runs)
				ts.Score, ts.CostUSD, ts.PassRate = ts.Score/n, ts.CostUSD/n, ts.PassRate/n
				vs.Mean += ts.Score
				vs.CostUSD += ts.CostUSD
			}
			vs.Tasks = append(vs.Tasks, *ts)
		}
		if n := float64(len(ids) - len(vs.Missing)); n > 0 {
			vs.Mean /= n
			vs.CostUSD /= n
		}
		out = append(out, vs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

// Adoption thresholds (the plan's rule; harness2 does not publish one).
const (
	MinGain     = 1.0  // points on the mean
	MaxCostRise = 0.10 // fraction per task
)

// Verdict says whether cand may be proposed for adoption over base. It is a
// proposal gate only: a human still approves the diff in Plannotator.
type Verdict struct {
	Base        string   `json:"base"`
	Cand        string   `json:"cand"`
	Compared    []string `json:"compared"` // tasks scored under both
	BaseMean    float64  `json:"base_mean"`
	CandMean    float64  `json:"cand_mean"`
	Delta       float64  `json:"delta"`
	BaseCost    float64  `json:"base_cost_usd"`
	CandCost    float64  `json:"cand_cost_usd"`
	Regressions []string `json:"regressions,omitempty"` // PASS under base, FAIL under cand
	Propose     bool     `json:"propose"`
	Reasons     []string `json:"reasons"`
}

// Compare applies the adoption rule over the tasks both versions scored.
// Every task base scored must also be scored under cand: a candidate cannot
// win by breaking the evals it would lose.
func Compare(base, cand VersionScore) Verdict {
	v := Verdict{Base: base.Version, Cand: cand.Version}
	bt, ct := byTask(base), byTask(cand)
	for _, id := range sortedKeys(bt) {
		b := bt[id]
		c, ok := ct[id]
		if !ok {
			v.Reasons = append(v.Reasons, fmt.Sprintf("%s is scored under base but not under cand", id))
			continue
		}
		v.Compared = append(v.Compared, id)
		v.BaseMean += b.Score
		v.CandMean += c.Score
		v.BaseCost += b.CostUSD
		v.CandCost += c.CostUSD
		if b.Pass() && !c.Pass() {
			v.Regressions = append(v.Regressions, id)
		}
	}
	if n := float64(len(v.Compared)); n > 0 {
		v.BaseMean, v.CandMean, v.BaseCost, v.CandCost = v.BaseMean/n, v.CandMean/n, v.BaseCost/n, v.CandCost/n
	} else {
		v.Reasons = append(v.Reasons, "no task is scored under both versions")
	}
	v.Delta = v.CandMean - v.BaseMean
	if v.Delta < MinGain {
		v.Reasons = append(v.Reasons, fmt.Sprintf("gain %.2f < %.0f point", v.Delta, MinGain))
	}
	if len(v.Regressions) > 0 {
		v.Reasons = append(v.Reasons, "PASS→FAIL on "+strings.Join(v.Regressions, ", "))
	}
	if v.CandCost > v.BaseCost*(1+MaxCostRise)+1e-9 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("cost/task $%.4f > base $%.4f +%.0f%%", v.CandCost, v.BaseCost, MaxCostRise*100))
	}
	v.Propose = len(v.Reasons) == 0
	if v.Propose {
		v.Reasons = []string{fmt.Sprintf("+%.2f points over %d task(s), no regression, cost within +%.0f%%", v.Delta, len(v.Compared), MaxCostRise*100)}
	}
	return v
}

func byTask(vs VersionScore) map[string]TaskScore {
	m := map[string]TaskScore{}
	for _, t := range vs.Tasks {
		if t.Runs > 0 {
			m[t.Task] = t
		}
	}
	return m
}

func sortedKeys(m map[string]TaskScore) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}
