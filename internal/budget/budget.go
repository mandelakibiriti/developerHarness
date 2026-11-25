// Package budget tracks per-feature spend against the budgets in harness.json
// and reports when alert thresholds (e.g. 0.8, 1.0) are crossed. The harness
// turns a 100% crossing into a cycle warning; TokenTelemetry stays the cost of
// record for anything that disagrees.
package budget

import (
	"sort"
	"sync"
)

// Status is the budget state served to the shell.
type Status struct {
	Spent      float64   `json:"spent"`
	PerFeature float64   `json:"per_feature_usd"`
	Ratio      float64   `json:"ratio"`
	Over       bool      `json:"over"`
	AlertAt    []float64 `json:"alert_at"`
}

// Tracker accumulates cost and fires each alert threshold at most once.
type Tracker struct {
	mu         sync.Mutex
	perFeature float64
	alertAt    []float64
	spent      float64
	fired      map[float64]bool
}

// New builds a tracker. alertAt holds fractions of perFeature (0.8, 1.0).
func New(perFeature float64, alertAt []float64) *Tracker {
	return &Tracker{perFeature: perFeature, alertAt: alertAt, fired: map[float64]bool{}}
}

// Add records an amount and returns the thresholds crossed for the first time.
func (t *Tracker) Add(amount float64) []float64 {
	if amount <= 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.spent += amount
	var crossed []float64
	for _, a := range t.alertAt {
		if t.fired[a] {
			continue
		}
		if t.perFeature > 0 && t.spent >= t.perFeature*a {
			t.fired[a] = true
			crossed = append(crossed, a)
		}
	}
	sort.Float64s(crossed)
	return crossed
}

// Over reports whether the 100% threshold has fired.
func (t *Tracker) Over() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fired[1.0]
}

// Status returns a snapshot of spend, ratio and alert state.
func (t *Tracker) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	var ratio float64
	if t.perFeature > 0 {
		ratio = t.spent / t.perFeature
	}
	return Status{
		Spent:      t.spent,
		PerFeature: t.perFeature,
		Ratio:      ratio,
		Over:       t.fired[1.0],
		AlertAt:    t.alertAt,
	}
}
