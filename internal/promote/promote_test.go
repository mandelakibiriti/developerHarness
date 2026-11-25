package promote

import (
	"testing"

	"github.com/devex/harness/internal/events"
)

func gate(result string) events.Event {
	return events.Event{Ev: "gate", Data: map[string]any{"result": result}}
}

func gateRung(result string, rung float64) events.Event {
	return events.Event{Ev: "gate", Data: map[string]any{"result": result, "hint_rung": rung}}
}

func finding(sev string) events.Event {
	return events.Event{Ev: "finding", Data: map[string]any{"severity": sev}}
}

func TestBeginnerPromotion(t *testing.T) {
	next, ready, _ := Evaluate("beginner", []events.Event{gate("pass"), gate("pass")})
	if !ready || next != "intermediate" {
		t.Fatalf("want intermediate ready, got %q ready=%v", next, ready)
	}
}

func TestBeginnerNotEnoughGates(t *testing.T) {
	_, ready, _ := Evaluate("beginner", []events.Event{gate("pass")})
	if ready {
		t.Fatal("one gate is not enough")
	}
}

func TestFailResetsRun(t *testing.T) {
	_, ready, _ := Evaluate("beginner", []events.Event{gate("pass"), gate("pass"), gate("fail"), gate("pass")})
	if ready {
		t.Fatal("a FAIL resets the consecutive run")
	}
}

func TestHighRungResetsRun(t *testing.T) {
	_, ready, _ := Evaluate("beginner", []events.Event{gate("pass"), gateRung("pass", 3), gate("pass")})
	if ready {
		t.Fatal("hint-ladder rung above 2 resets the run")
	}
}

func TestIntermediateBlockedByHighFinding(t *testing.T) {
	_, ready, _ := Evaluate("intermediate", []events.Event{gate("pass"), gate("pass"), finding("high")})
	if ready {
		t.Fatal("High finding blocks promotion to advanced")
	}
}

func TestIntermediatePromotes(t *testing.T) {
	next, ready, _ := Evaluate("intermediate", []events.Event{gate("pass"), gate("pass"), finding("low")})
	if !ready || next != "advanced" {
		t.Fatalf("want advanced ready, got %q ready=%v", next, ready)
	}
}

func TestAdvancedStays(t *testing.T) {
	next, ready, _ := Evaluate("advanced", []events.Event{gate("pass"), gate("pass")})
	if ready || next != "advanced" {
		t.Fatalf("advanced should stay, got %q ready=%v", next, ready)
	}
}
