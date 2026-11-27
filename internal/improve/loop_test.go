package improve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devex/harness/internal/config"
)

func TestApplyCandidateWritesLearnable(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Schema: config.SchemaVersion}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyCandidate(root, cfg, Candidate{ID: "a", File: "rules/new.md", Content: "# new"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "rules", "new.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "# new" {
		t.Fatalf("content = %q, want %q", string(b), "# new")
	}
}

func TestApplyCandidateRejectsProtected(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Schema: config.SchemaVersion}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Candidate{
		{ID: "b", File: "harness.json", Content: "{}"},
		{ID: "c", File: "practice-problems/x.json", Content: "{}"},
		{ID: "d", File: "../escape.md", Content: "# x"},
	} {
		if err := ApplyCandidate(root, cfg, c); err == nil {
			t.Errorf("ApplyCandidate(%q) = nil, want error", c.File)
		}
	}
}

func TestApplyAllPartition(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Schema: config.SchemaVersion}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	rejected := ApplyAll(root, cfg, []Candidate{
		{ID: "ok", File: "rules/a.md", Content: "x"},
		{ID: "bad", File: "harness.json", Content: "{}"},
	})
	if len(rejected) != 1 || rejected[0].ID != "bad" {
		t.Fatalf("rejected = %+v, want exactly [bad]", rejected)
	}
	if _, err := os.Stat(filepath.Join(root, "rules", "a.md")); err != nil {
		t.Fatalf("rules/a.md not written: %v", err)
	}
}

func TestResolveProposer(t *testing.T) {
	cfg := &config.Config{Schema: config.SchemaVersion}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	p, err := ResolveProposer(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.Name() != "claude" {
		t.Fatalf("default proposer = %v, want name %q", p, "claude")
	}

	cfg2 := &config.Config{Schema: config.SchemaVersion, Improve: config.Improve{Proposer: "bogus"}}
	if _, err := ResolveProposer(cfg2, t.TempDir()); err == nil {
		t.Fatal("ResolveProposer with bogus proposer = nil error, want error")
	}
}
