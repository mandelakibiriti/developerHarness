package track

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "beginner.json")
	content := `{
		"level":"beginner","profile":"core",
		"primary_tabs":["Map","Learn"],"hidden_tabs":["Trail","Spend","Secure"],
		"gates":["language","map-update"],
		"floorplan_audience":"everyone","agent_mode":"tutor"
	}`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Profile != "core" || tr.AgentMode != "tutor" {
		t.Fatalf("unexpected track: %+v", tr)
	}
	if len(tr.HiddenTabs) != 3 {
		t.Fatalf("hidden tabs: %v", tr.HiddenTabs)
	}
}

func TestLoadRejectsBadAgentMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte(`{"level":"x","profile":"core","agent_mode":"nope"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for bad agent_mode")
	}
}
