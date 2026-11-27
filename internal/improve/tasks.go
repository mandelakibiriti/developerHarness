// Package improve is the harness's self-improvement layer, ported from
// harness2: an eval set with a seeded train/test split, a deterministic judge
// over the event log, and the comparison that decides whether a candidate
// harness version may be proposed for adoption.
//
// A run of one eval task is bracketed in the log:
//
//	eval-start {run, task, harness_version}       harness improve begin
//	...events stamped data.run = $HARNESS_RUN     hook, gate, cost, ...
//	eval-end   {run, status, pass, exit_code}     harness improve end (runs the task's test itself)
//
// The judge trusts only eval-end's pass, which the harness produced by running
// the real test, never a gate the agent recorded.
package improve

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Task is one eval task: a prompt for the agent and the practice problem whose
// test decides pass/fail.
type Task struct {
	ID      string `json:"id"`
	Prompt  string `json:"prompt"`
	Problem string `json:"problem"`          // practice-problems/<problem>.json
	Mode    string `json:"mode,omitempty"`   // compiler mode, default "build"
	Level   string `json:"level,omitempty"`  // default: harness level
	Domain  string `json:"domain,omitempty"` // lessons domain
}

var taskID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// LoadTasks reads evals/tasks/*.json under root, sorted by id.
func LoadTasks(root string) ([]Task, error) {
	paths, err := filepath.Glob(filepath.Join(root, "evals", "tasks", "*.json"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Task
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if !taskID.MatchString(t.ID) || !taskID.MatchString(t.Problem) || t.Prompt == "" {
			return nil, fmt.Errorf("%s: id, problem (lowercase ids) and prompt are required", p)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("%s: duplicate task id %q", p, t.ID)
		}
		seen[t.ID] = true
		if t.Mode == "" {
			t.Mode = "build"
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Split is a seeded train/test partition of task ids.
type Split struct {
	Seed  int64    `json:"seed"`
	Train []string `json:"train"`
	Test  []string `json:"test"`
}

// NewSplit orders ids by sha256(seed:id) and puts the first round(n*testFrac)
// in test (at least one when n >= 2). Same seed and ids, same split; adding a
// task never reshuffles how the others rank.
func NewSplit(ids []string, seed int64, testFrac float64) Split {
	keyed := make([]string, len(ids))
	copy(keyed, ids)
	key := func(id string) string {
		h := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", seed, id)))
		return hex.EncodeToString(h[:])
	}
	sort.Slice(keyed, func(i, j int) bool { return key(keyed[i]) < key(keyed[j]) })
	nTest := int(math.Round(float64(len(keyed)) * testFrac))
	if nTest == 0 && len(keyed) >= 2 && testFrac > 0 {
		nTest = 1
	}
	s := Split{Seed: seed, Test: append([]string{}, keyed[:nTest]...), Train: append([]string{}, keyed[nTest:]...)}
	sort.Strings(s.Train)
	sort.Strings(s.Test)
	return s
}

// Of returns the ids in the named split: "train", "test", or "all".
func (s Split) Of(name string) ([]string, error) {
	switch name {
	case "train":
		return s.Train, nil
	case "test":
		return s.Test, nil
	case "all", "":
		return append(append([]string{}, s.Train...), s.Test...), nil
	}
	return nil, fmt.Errorf("split must be train, test or all, not %q", name)
}

// SplitPath is where the split lives, versioned with the eval set.
func SplitPath(root string) string { return filepath.Join(root, "evals", "split.json") }

// LoadSplit reads evals/split.json.
func LoadSplit(root string) (Split, error) {
	var s Split
	data, err := os.ReadFile(SplitPath(root))
	if err != nil {
		return s, fmt.Errorf("read split (run `harness improve split` first): %w", err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", SplitPath(root), err)
	}
	return s, nil
}
