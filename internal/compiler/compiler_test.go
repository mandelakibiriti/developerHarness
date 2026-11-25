package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devex/harness/context"
	"github.com/devex/harness/internal/config"
)

// TestCompileRepo compiles against this repo's real files: the golden check
// that the build/beginner prompt carries the tutor text, rule ids, the track
// and the skill index, and that the version is stable.
func TestCompileRepo(t *testing.T) {
	root, _ := filepath.Abs("../..")
	cfg, err := config.Load(filepath.Join(root, "harness.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := &Compiler{Root: root, Cfg: cfg, Defaults: harnessctx.FS}
	b, err := c.Compile(Request{Mode: "build", Level: "beginner"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"You are the DevEx Harness guide", // base
		"test-driven development",         // build
		"hint ladder",                     // build.beginner
		"API-3",                           // rule index
		"Gates required before a card is done: language, map-update",
		"- rule-gate:", // skill index
		"## Guardrails",
	} {
		if !strings.Contains(b.Prefix, want) {
			t.Errorf("prefix missing %q", want)
		}
	}
	if strings.Contains(b.Prefix, "a response that grows with the data") { // API-2 Prevents: line
		t.Error("rule index should carry the Rule: line only")
	}
	again, _ := c.Compile(Request{Mode: "build", Level: "beginner"})
	if again.Version != b.Version || len(b.Version) != 12 {
		t.Fatalf("version not stable: %q vs %q", b.Version, again.Version)
	}
	other, _ := c.Compile(Request{Mode: "build", Level: "advanced"})
	if other.Version == b.Version {
		t.Fatal("different context must give a different version")
	}
}

func TestEmbeddedDefaultsAndOverride(t *testing.T) {
	root := t.TempDir()
	c := &Compiler{Root: root, Defaults: harnessctx.FS}
	b, err := c.Compile(Request{Mode: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.Prefix, "modular monolith") || b.Sources[0] != "embedded:context/system/base.md" {
		t.Fatalf("embedded defaults not used: %v", b.Sources)
	}
	write(t, root, "context/system/base.md", "Project-specific base.")
	b2, _ := c.Compile(Request{Mode: "architect"})
	if !strings.HasPrefix(b2.Prefix, "Project-specific base.") || b2.Version == b.Version {
		t.Fatalf("project override did not win: %q", b2.Prefix[:40])
	}
}

func TestLessonsSuffix(t *testing.T) {
	root := t.TempDir()
	var lines []string
	for i := 1; i <= 7; i++ {
		lines = append(lines, fmt.Sprintf(`{"lesson":"lesson %d","evidence":["ev%d"]}`, i, i))
	}
	write(t, root, "context/memory/lessons/go.jsonl", strings.Join(lines, "\n")+"\n")
	c := &Compiler{Root: root, Defaults: harnessctx.FS}
	b, err := c.Compile(Request{Domain: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.Suffix, "lesson 2\n") || !strings.Contains(b.Suffix, "lesson 3") || !strings.Contains(b.Suffix, "lesson 7") {
		t.Fatalf("want the last 5 lessons, got %q", b.Suffix)
	}
	if !strings.HasSuffix(b.System(), b.Suffix) {
		t.Fatal("System() must end with the per-turn suffix")
	}
	if _, err := c.Compile(Request{Domain: "../etc"}); err == nil {
		t.Fatal("path-like domain must be rejected")
	}
	write(t, root, "context/memory/lessons/bad.jsonl", "not json\n")
	if _, err := c.Compile(Request{Domain: "bad"}); err == nil {
		t.Fatal("malformed lesson must fail loud")
	}
}

func TestMissingRulePackFails(t *testing.T) {
	c := &Compiler{Root: t.TempDir(), Cfg: &config.Config{Rules: []string{"rules/nope.md"}}, Defaults: harnessctx.FS}
	if _, err := c.Compile(Request{}); err == nil {
		t.Fatal("a configured but missing rule pack must be an error")
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
