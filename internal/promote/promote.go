// Package promote decides when a level should advance, on evidence not time.
//
//	beginner     -> intermediate : >= 2 consecutive PASS gate events with no
//	                               hint-ladder rung above 2.
//	intermediate -> advanced     : the above, plus no High finding across the
//	                               features covered by the run.
//
// The evidence is the shared event log, which is why gate and finding events
// are first-class.
package promote

import (
	"fmt"
	"strings"

	"github.com/devex/harness/internal/events"
)

const passRun = 2

// Evaluate returns the recommended next level, whether promotion is ready, and
// a one-line reason.
func Evaluate(level string, evs []events.Event) (next string, ready bool, reason string) {
	switch level {
	case "advanced":
		return "advanced", false, "already at advanced"
	case "beginner", "intermediate":
	default:
		return level, false, "unknown level " + level
	}

	consecutivePass := 0
	highFindings := 0
	for _, ev := range evs {
		switch ev.Ev {
		case "gate":
			result, _ := ev.Data["result"].(string)
			rung, _ := ev.Data["hint_rung"].(float64)
			switch result {
			case "pass", "PASS", "ok":
				if rung > 2 {
					consecutivePass = 0 // a hint-ladder rung above 2 resets the run
					continue
				}
				consecutivePass++
			case "fail", "FAIL":
				consecutivePass = 0
			}
		case "finding":
			if sev, _ := ev.Data["severity"].(string); strings.EqualFold(sev, "high") {
				highFindings++
			}
		}
	}

	if consecutivePass < passRun {
		return level, false, fmt.Sprintf("need %d consecutive PASS gates (have %d)", passRun, consecutivePass)
	}
	if level == "beginner" {
		return "intermediate", true, fmt.Sprintf("%d consecutive PASS gates, no hint rung above 2", consecutivePass)
	}
	if highFindings > 0 {
		return "intermediate", false, fmt.Sprintf("%d High finding(s) block promotion to advanced", highFindings)
	}
	return "advanced", true, fmt.Sprintf("%d consecutive PASS gates and no High findings", consecutivePass)
}
