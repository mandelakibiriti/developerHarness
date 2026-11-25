// Package track loads the per-level track (tracks/<level>.json), which maps a
// level to a profile, the tabs to show/hide, the gates required, the Floorplan
// audience and the agent mode. New levels are a track file, not code.
package track

import (
	"encoding/json"
	"fmt"
	"os"
)

type Track struct {
	Level             string   `json:"level"`
	Profile           string   `json:"profile"`
	PrimaryTabs       []string `json:"primary_tabs"`
	HiddenTabs        []string `json:"hidden_tabs"`
	Gates             []string `json:"gates"`
	FloorplanAudience string   `json:"floorplan_audience"`
	AgentMode         string   `json:"agent_mode"`
}

// Load reads and validates a track file.
func Load(path string) (*Track, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var t Track
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &t, nil
}

func (t *Track) Validate() error {
	if t.Level == "" {
		return fmt.Errorf("track has empty level")
	}
	if t.Profile == "" {
		return fmt.Errorf("track %q has empty profile", t.Level)
	}
	switch t.AgentMode {
	case "", "tutor", "review", "autonomous":
	default:
		return fmt.Errorf("track %q has unknown agent_mode %q", t.Level, t.AgentMode)
	}
	return nil
}
