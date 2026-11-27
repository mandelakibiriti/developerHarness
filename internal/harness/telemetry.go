package harness

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/devex/harness/internal/events"
)

// --- /api/telemetry: project-level LLM usage built from .harness/events.jsonl ---
//
// Every model call already logs llm-request / llm-usage / llm-error (and cost
// when priced), so the project's own event log is the telemetry source; no
// external collector is needed. shortcut: re-reads the whole log per request,
// fine for thousands of events; switch to incremental aggregation if it grows large.

type usageAgg struct {
	Name             string  `json:"name"`
	Calls            int     `json:"calls"`
	Errors           int     `json:"errors"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	AvgLatencyMS     int64   `json:"avg_latency_ms"`
	latencySum       int64
	latencyN         int64
}

func (a *usageAgg) addUsage(prompt, completion int, ms int64) {
	a.Calls++
	a.PromptTokens += prompt
	a.CompletionTokens += completion
	if ms > 0 {
		a.latencySum += ms
		a.latencyN++
		a.AvgLatencyMS = a.latencySum / a.latencyN
	}
}

type dayAgg struct {
	Date             string `json:"date"` // local YYYY-MM-DD
	Calls            int    `json:"calls"`
	Errors           int    `json:"errors"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
}

type callRecord struct {
	T                string `json:"t"`
	Status           string `json:"status"` // ok | error | cancelled
	Purpose          string `json:"purpose"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	DurationMS       int64  `json:"duration_ms,omitempty"`
	Preview          string `json:"preview,omitempty"`
	Error            string `json:"error,omitempty"`
}

type telemetry struct {
	WindowDays int            `json:"window_days"`
	Totals     usageAgg       `json:"totals"`
	Cancelled  int            `json:"cancelled"`
	KeyChecks  int            `json:"key_checks"`
	Today      dayAgg         `json:"today"`
	Daily      []dayAgg       `json:"daily"`
	ByFeature  []*usageAgg    `json:"by_feature"`
	ByModel    []*usageAgg    `json:"by_model"`
	Recent     []callRecord   `json:"recent"`
	Activity   map[string]int `json:"activity"`
	Budget     any            `json:"budget,omitempty"`
}

const recentCalls = 25

// aggregate folds events into project telemetry; now fixes "today" and the window.
func aggregate(evs []events.Event, days int, now time.Time) telemetry {
	tel := telemetry{WindowDays: days, Totals: usageAgg{Name: "all"}, Activity: map[string]int{}}
	byFeature, byModel := map[string]*usageAgg{}, map[string]*usageAgg{}
	daily := map[string]*dayAgg{}
	start := now.AddDate(0, 0, -(days - 1))
	for d := 0; d < days; d++ {
		key := start.AddDate(0, 0, d).Format("2006-01-02")
		daily[key] = &dayAgg{Date: key}
		tel.Daily = append(tel.Daily, dayAgg{Date: key})
	}
	todayKey := now.Format("2006-01-02")
	get := func(m map[string]*usageAgg, k string) *usageAgg {
		if m[k] == nil {
			m[k] = &usageAgg{Name: k}
		}
		return m[k]
	}
	// shortcut: pairs a result with the latest request for the same purpose+model;
	// overlapping identical calls could swap previews. Add a call id if that matters.
	previews := map[string]string{}
	var calls []callRecord

	for _, ev := range evs {
		d := ev.Data
		purpose, provider, model := str(d["purpose"]), str(d["provider"]), str(d["model"])
		modelKey := provider + "/" + model
		day := ""
		if t, err := time.Parse(time.RFC3339, ev.T); err == nil {
			day = t.In(now.Location()).Format("2006-01-02")
		}
		switch ev.Ev {
		case "llm-request":
			previews[purpose+"|"+modelKey] = str(d["preview"])
		case "llm-usage":
			p, c, ms := num(d["prompt_tokens"]), num(d["completion_tokens"]), int64(num(d["duration_ms"]))
			tel.Totals.addUsage(p, c, ms)
			get(byFeature, purpose).addUsage(p, c, ms)
			get(byModel, modelKey).addUsage(p, c, ms)
			if da := daily[day]; da != nil {
				da.Calls++
				da.PromptTokens += p
				da.CompletionTokens += c
			}
			calls = append(calls, callRecord{T: ev.T, Status: "ok", Purpose: purpose, Provider: provider, Model: model,
				PromptTokens: p, CompletionTokens: c, DurationMS: ms, Preview: previews[purpose+"|"+modelKey]})
		case "llm-error":
			status := "error"
			if b, _ := d["cancelled"].(bool); b {
				status = "cancelled"
				tel.Cancelled++
			} else {
				tel.Totals.Errors++
				get(byFeature, purpose).Errors++
				get(byModel, modelKey).Errors++
				if da := daily[day]; da != nil {
					da.Errors++
				}
			}
			calls = append(calls, callRecord{T: ev.T, Status: status, Purpose: purpose, Provider: provider, Model: model,
				DurationMS: int64(num(d["duration_ms"])), Preview: previews[purpose+"|"+modelKey], Error: str(d["error"])})
		case "cost":
			amt, _ := d["amount"].(float64)
			tel.Totals.CostUSD += amt
			get(byFeature, str(d["feature"])).CostUSD += amt
		case "llm-key-check":
			tel.KeyChecks++
		case "spec-generated", "run", "review-finished", "doc-write", "floorplan-generated":
			tel.Activity[ev.Ev]++
		}
	}

	for i := range tel.Daily {
		tel.Daily[i] = *daily[tel.Daily[i].Date]
	}
	if t := daily[todayKey]; t != nil {
		tel.Today = *t
	}
	tel.ByFeature, tel.ByModel = sortedAggs(byFeature), sortedAggs(byModel)
	for i := len(calls) - 1; i >= 0 && len(tel.Recent) < recentCalls; i-- {
		tel.Recent = append(tel.Recent, calls[i])
	}
	if tel.Recent == nil {
		tel.Recent = []callRecord{}
	}
	return tel
}

func sortedAggs(m map[string]*usageAgg) []*usageAgg {
	out := make([]*usageAgg, 0, len(m))
	for _, a := range m {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := out[i].PromptTokens+out[i].CompletionTokens, out[j].PromptTokens+out[j].CompletionTokens
		if ti != tj {
			return ti > tj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) int { f, _ := v.(float64); return int(f) }

func (rt *Runtime) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	days := 14
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && n >= 1 && n <= 90 {
		days = n
	}
	evs, err := events.ReadAll(rt.log.Path())
	if err != nil {
		http.Error(w, "could not read event log: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tel := aggregate(evs, days, time.Now())
	if rt.budget != nil {
		tel.Budget = rt.budget.Status()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tel)
}
