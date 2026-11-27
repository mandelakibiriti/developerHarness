package improve

import (
	"context"
	"testing"
	"time"

	"github.com/devex/harness/internal/config"
)

func newTestCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{Schema: config.SchemaVersion, Improve: config.Improve{Proposer: "claude", K: 5}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestClaudeProposerParsesAndFilters(t *testing.T) {
	root := t.TempDir()
	cfg := newTestCfg(t)
	p := NewClaudeProposer(root, cfg)
	p.command = func(ctx context.Context, prompt string) (string, error) {
		return `[
			{"id":"a","file":"rules/go.md","content":"# go","why":"x"},
			{"id":"b","file":"harness.json","content":"{}","why":"bad"},
			{"file":"context/system/build.md","content":"# build","why":"y"}
		]`, nil
	}
	got, err := p.Propose(context.Background(), ProposeRequest{Root: root, Base: "v1", K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
	for i := range got {
		if err := ValidateCandidate(cfg, got[i]); err != nil {
			t.Errorf("candidate %d fails validation: %v", i, err)
		}
	}
}

func TestClaudeProposerTrimsToK(t *testing.T) {
	root := t.TempDir()
	cfg := newTestCfg(t)
	p := NewClaudeProposer(root, cfg)
	p.command = func(ctx context.Context, prompt string) (string, error) {
		return `[
			{"id":"a","file":"rules/go.md","content":"# a","why":"x"},
			{"id":"b","file":"rules/python.md","content":"# b","why":"y"},
			{"id":"c","file":"rules/js.md","content":"# c","why":"z"}
		]`, nil
	}
	got, err := p.Propose(context.Background(), ProposeRequest{Root: root, Base: "v1", K: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
}

func TestClaudeProposerBadOutput(t *testing.T) {
	root := t.TempDir()
	cfg := newTestCfg(t)
	p := NewClaudeProposer(root, cfg)
	p.command = func(ctx context.Context, prompt string) (string, error) {
		return "not json at all", nil
	}
	if _, err := p.Propose(context.Background(), ProposeRequest{Root: root, Base: "v1", K: 5}); err == nil {
		t.Fatal("expected error for non-JSON output")
	}
}

func TestClaudeProposerAppliesTimeout(t *testing.T) {
	root := t.TempDir()
	cfg := newTestCfg(t)
	cfg.Improve.Timeout = "2m"
	p := NewClaudeProposer(root, cfg)
	if p.timeout != 2*time.Minute {
		t.Fatalf("timeout = %v, want 2m", p.timeout)
	}
	var sawDeadline bool
	p.command = func(ctx context.Context, prompt string) (string, error) {
		if _, ok := ctx.Deadline(); ok {
			sawDeadline = true
		}
		return `[{"id":"a","file":"rules/go.md","content":"x","why":"y"}]`, nil
	}
	if _, err := p.Propose(context.Background(), ProposeRequest{Root: root, Base: "v1", K: 3}); err != nil {
		t.Fatal(err)
	}
	if !sawDeadline {
		t.Error("Propose did not apply a deadline to the command context")
	}
}

func TestProposerTimeout(t *testing.T) {
	if proposerTimeout("") != defaultProposerTimeout {
		t.Error("empty should default to 5m")
	}
	if proposerTimeout("30s") != 30*time.Second {
		t.Error("30s should parse")
	}
	if proposerTimeout("bogus") != defaultProposerTimeout {
		t.Error("bogus should default")
	}
	if proposerTimeout("-1s") != defaultProposerTimeout {
		t.Error("non-positive should default")
	}
}
