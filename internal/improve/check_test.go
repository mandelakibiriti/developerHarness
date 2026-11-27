package improve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/devex/harness/internal/compiler"
	"github.com/devex/harness/internal/config"
)

// setupCheckRoot builds a minimal project root with a valid eval set and one
// practice problem, and returns the root and a config + compiler over it.
func setupCheckRoot(t *testing.T, withProblem bool) (string, *config.Config, *compiler.Compiler) {
	t.Helper()
	root := t.TempDir()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("evals/tasks/a.json", `{"id":"a","prompt":"do a","problem":"pa"}`)
	write("evals/split.json", `{"seed":42,"train":["a"],"test":[]}`)
	if withProblem {
		write("practice-problems/pa.json", `{"id":"pa","skill":"x","goal":"g","process":["true"]}`)
	}
	cfg := &config.Config{Schema: config.SchemaVersion}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	comp := &compiler.Compiler{
		Root:     root,
		Cfg:      cfg,
		Defaults: fstest.MapFS{"system/base.md": &fstest.MapFile{Data: []byte("base")}},
	}
	return root, cfg, comp
}

func byName(items []CheckItem) map[string]CheckItem {
	m := map[string]CheckItem{}
	for _, it := range items {
		m[it.Name] = it
	}
	return m
}

func TestCheckPasses(t *testing.T) {
	root, cfg, comp := setupCheckRoot(t, true)
	items := Check(context.Background(), root, cfg, CheckOptions{
		Mode: "build", Level: "beginner", SkipTests: true, Compiler: comp,
	})
	m := byName(items)
	for _, name := range []string{"context compile", "eval tasks", "eval split", "practice problems"} {
		if it, ok := m[name]; !ok || !it.OK {
			t.Errorf("%s: %+v", name, it)
		}
	}
	if _, ok := m["go vet"]; ok {
		t.Error("go vet should be skipped with SkipTests")
	}
	if _, ok := m["go test -race"]; ok {
		t.Error("go test -race should be skipped with SkipTests")
	}
}

func TestCheckFlagsMissingPracticeProblem(t *testing.T) {
	root, cfg, comp := setupCheckRoot(t, false)
	items := Check(context.Background(), root, cfg, CheckOptions{
		Mode: "build", Level: "beginner", SkipTests: true, Compiler: comp,
	})
	if it := byName(items)["practice problems"]; it.OK {
		t.Errorf("missing practice problem should fail the check: %+v", it)
	}
}

func TestCheckFlagsBrokenAdapter(t *testing.T) {
	root, cfg, comp := setupCheckRoot(t, true)
	// A missing required context file makes Compile fail.
	comp.Defaults = fstest.MapFS{} // no system/base.md
	items := Check(context.Background(), root, cfg, CheckOptions{
		Mode: "build", Level: "beginner", SkipTests: true, Compiler: comp,
	})
	if it := byName(items)["context compile"]; it.OK {
		t.Errorf("missing base context should fail the compile check: %+v", it)
	}
}
