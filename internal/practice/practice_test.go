package practice

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsEmptyProcess(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte(`{"id":"bad","goal":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for empty process")
	}
}

func TestRunPass(t *testing.T) {
	p := &Problem{ID: "ok", Process: []string{"sh", "-c", "echo hi && exit 0"}}
	r := p.Run()
	if !r.Pass || r.ExitCode != 0 {
		t.Fatalf("result: %+v", r)
	}
	if r.Output != "hi" {
		t.Fatalf("output %q", r.Output)
	}
}

func TestRunFail(t *testing.T) {
	p := &Problem{ID: "bad", Process: []string{"sh", "-c", "echo wrong; exit 1"}}
	r := p.Run()
	if r.Pass || r.ExitCode != 1 {
		t.Fatalf("result: %+v", r)
	}
}

func TestRunMissingCommand(t *testing.T) {
	p := &Problem{ID: "nope", Process: []string{"definitely-not-a-real-command-xyz"}}
	r := p.Run()
	if r.Pass {
		t.Fatalf("missing command should fail, got %+v", r)
	}
	if r.Error == "" {
		t.Fatal("expected an error message")
	}
}

func TestRunScrubsEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("HARNESS_RUN", "keepme")
	p := &Problem{ID: "scrub", Process: []string{"sh", "-c", `printf '%s|%s' "$ANTHROPIC_API_KEY" "$HARNESS_RUN"`}}
	r := p.Run()
	if !r.Pass {
		t.Fatalf("result: %+v", r)
	}
	if r.Output != "|keepme" {
		t.Fatalf("output %q: a credential leaked or a benign variable was stripped", r.Output)
	}
}
