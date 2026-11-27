package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnessctx "github.com/devex/harness/context"
	"github.com/devex/harness/internal/compiler"
	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/track"
)

func TestInitMakesIndependentWorkingProjects(t *testing.T) {
	t.Setenv("HARNESS_HOME", t.TempDir())
	goDir, webDir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module x\n"), 0o644)
	os.WriteFile(filepath.Join(webDir, "package.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(webDir, ".gitignore"), []byte("node_modules"), 0o644)
	os.MkdirAll(filepath.Join(webDir, ".claude"), 0o755)
	os.WriteFile(filepath.Join(webDir, ".claude", "settings.json"), []byte(`{"mine":true}`), 0o644)

	a, err := Init(goDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Init(webDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.Port == b.Port {
		t.Fatalf("both projects got port %d", a.Port)
	}

	for dir, rule := range map[string]string{goDir: "rules/go.md", webDir: "rules/web.md"} {
		cfg, err := config.Load(filepath.Join(dir, "harness.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !contains(cfg.Rules, rule) {
			t.Errorf("%s: rules %v lack %s", dir, cfg.Rules, rule)
		}
		for _, level := range []string{"beginner", "intermediate", "advanced"} {
			if _, err := track.Load(filepath.Join(dir, "tracks", level+".json")); err != nil {
				t.Error(err)
			}
		}
		c := &compiler.Compiler{Root: dir, Cfg: cfg, Defaults: harnessctx.FS}
		if _, err := c.Compile(compiler.Request{Mode: "build", Level: "beginner"}); err != nil {
			t.Errorf("%s: context does not compile: %v", dir, err)
		}
		ig, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
		if !strings.Contains("\n"+string(ig), "\n.harness/\n") {
			t.Errorf("%s: .harness/ not ignored: %q", dir, ig)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(webDir, ".claude", "settings.json")); string(got) != `{"mine":true}` {
		t.Errorf("existing settings overwritten: %s", got)
	}
	if !contains(b.Kept, ".claude/settings.json") {
		t.Errorf("kept = %v", b.Kept)
	}
	if _, err := Init(goDir, 0); err == nil {
		t.Error("second init of the same project must refuse")
	}
	if list, _ := Projects(); len(list) != 2 {
		t.Errorf("registry = %v", list)
	}
}
