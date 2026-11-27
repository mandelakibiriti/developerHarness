package improve

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/devex/harness/internal/practice"
)

// An eval task measures nothing unless its test fails on the untouched tree
// (and fails as a test, not because the setup is broken).
func TestEvalFixturesFailBeforeTheWork(t *testing.T) {
	root := filepath.Join("..", "..")
	tasks, err := LoadTasks(root)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("tasks: %d, %v", len(tasks), err)
	}
	for _, task := range tasks {
		p, err := practice.Load(filepath.Join(root, "practice-problems", task.Problem+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if p.Cwd == "" || p.Cwd == "." {
			t.Errorf("%s has no folder of its own", task.ID)
			continue
		}
		if _, err := exec.LookPath(p.Process[0]); err != nil {
			t.Logf("%s skipped: %s not installed", task.ID, p.Process[0])
			continue
		}
		p.Cwd = filepath.Join(root, p.Cwd)
		if res := p.Run(); res.Pass || res.Error != "" {
			t.Errorf("%s: pass=%v error=%q before any work\n%s", task.ID, res.Pass, res.Error, res.Output)
		}
	}
}
