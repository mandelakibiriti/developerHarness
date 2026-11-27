package harness

import (
	"testing"
	"time"

	"github.com/devex/harness/internal/events"
)

func TestAggregateTelemetry(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	ev := func(t, name string, data map[string]any) events.Event {
		return events.Event{T: t, Ev: name, Data: data}
	}
	evs := []events.Event{
		ev("2026-10-01T00:00:00Z", "llm-usage", map[string]any{"purpose": "chat", "provider": "x", "model": "old", "prompt_tokens": 7.0}), // outside window
		ev("2026-11-25T09:00:00Z", "llm-usage", map[string]any{"purpose": "docs-generate", "provider": "nvidia", "model": "m", "prompt_tokens": 1000.0, "completion_tokens": 3000.0, "duration_ms": 60000.0}),
		ev("2026-11-25T09:01:00Z", "cost", map[string]any{"amount": 0.5, "feature": "docs-generate"}),
		ev("2026-11-26T10:00:00Z", "llm-request", map[string]any{"purpose": "chat", "provider": "nvidia", "model": "m", "preview": "hello"}),
		ev("2026-11-26T10:00:02Z", "llm-usage", map[string]any{"purpose": "chat", "provider": "nvidia", "model": "m", "prompt_tokens": 100.0, "completion_tokens": 50.0, "duration_ms": 2000.0}),
		ev("2026-11-26T11:00:00Z", "llm-error", map[string]any{"purpose": "chat", "provider": "nvidia", "model": "m", "error": "boom"}),
		ev("2026-11-26T11:05:00Z", "llm-error", map[string]any{"purpose": "docs-generate", "provider": "nvidia", "model": "m", "cancelled": true}),
		ev("2026-11-26T12:00:00Z", "spec-generated", nil),
	}
	tel := aggregate(evs, 7, now)

	if tel.Totals.Calls != 3 || tel.Totals.Errors != 1 || tel.Cancelled != 1 || tel.Totals.PromptTokens != 1107 || tel.Totals.CostUSD != 0.5 {
		t.Fatalf("totals = %+v cancelled=%d", tel.Totals, tel.Cancelled)
	}
	if tel.Today.Calls != 1 || tel.Today.Errors != 1 || tel.Today.PromptTokens != 100 {
		t.Fatalf("today = %+v", tel.Today)
	}
	if len(tel.Daily) != 7 || tel.Daily[6].Date != "2026-11-26" || tel.Daily[5].CompletionTokens != 3000 {
		t.Fatalf("daily = %+v", tel.Daily)
	}
	if tel.ByFeature[0].Name != "docs-generate" || tel.ByFeature[0].CostUSD != 0.5 || tel.ByFeature[0].AvgLatencyMS != 60000 {
		t.Fatalf("by feature = %+v", *tel.ByFeature[0])
	}
	if tel.Recent[0].Status != "cancelled" || tel.Recent[2].Preview != "hello" || tel.Recent[1].Error != "boom" {
		t.Fatalf("recent = %+v", tel.Recent)
	}
	if tel.Activity["spec-generated"] != 1 {
		t.Fatalf("activity = %v", tel.Activity)
	}
}
