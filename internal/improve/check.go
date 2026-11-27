package improve

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	harnessctx "github.com/devex/harness/context"
	"github.com/devex/harness/internal/compiler"
	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/practice"
)

// CheckItem is the outcome of one spec check.
type CheckItem struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// CheckOptions controls Check.
type CheckOptions struct {
	Mode      string             // compiler mode (default "build")
	Level     string             // compiler level
	Domain    string             // lessons domain
	SkipTests bool               // skip the go vet / go test -race gate
	Compiler  *compiler.Compiler // optional; nil builds the default (embedded context)
}

// Check is the improve loop's harness2-style spec gate: it establishes
// structural validity without calling a model and without writing to any
// harness file, so a broken candidate is rejected here instead of wasting a
// probe run that comes back unscored. It verifies four things, in cheap-first
// order: the adapter compiles to a stable version hash, the eval set and its
// split load, every task's practice problem (the real test) is present and
// runnable, and — unless skipped — the Go code vets and tests clean.
func Check(ctx context.Context, root string, cfg *config.Config, opts CheckOptions) []CheckItem {
	var items []CheckItem

	c := opts.Compiler
	if c == nil {
		c = &compiler.Compiler{Root: root, Cfg: cfg, Defaults: harnessctx.FS}
	}

	// 1. The adapter compiles, and to the same hash twice.
	req := compiler.Request{Mode: opts.Mode, Level: opts.Level, Domain: opts.Domain}
	first, err := c.Compile(req)
	if err != nil {
		items = append(items, CheckItem{Name: "context compile", Detail: err.Error(),
			Fix: "fix the context/ rules/ tracks/ or skills/ file named in the error"})
	} else if again, err := c.Compile(req); err != nil {
		items = append(items, CheckItem{Name: "context compile", Detail: "second compile failed: " + err.Error()})
	} else if again.Version != first.Version {
		items = append(items, CheckItem{Name: "context compile",
			Detail: fmt.Sprintf("not deterministic: %s then %s", first.Version, again.Version),
			Fix:    "remove any nondeterministic content from the context files"})
	} else {
		items = append(items, CheckItem{Name: "context compile", OK: true,
			Detail: fmt.Sprintf("version %s, %d sources", first.Version, len(first.Sources))})
	}

	// 2. The eval set and its split load.
	tasks, err := LoadTasks(root)
	if err != nil {
		items = append(items, CheckItem{Name: "eval tasks", Detail: err.Error(), Fix: "fix evals/tasks/*.json"})
	} else {
		items = append(items, CheckItem{Name: "eval tasks", OK: true, Detail: fmt.Sprintf("%d tasks", len(tasks))})
		if _, err := LoadSplit(root); err != nil {
			items = append(items, CheckItem{Name: "eval split", Detail: err.Error(), Fix: "run `harness improve split`"})
		} else {
			items = append(items, CheckItem{Name: "eval split", OK: true, Detail: "loads"})
		}

		// 3. Every task's practice problem (the test that decides pass/fail) is
		//    present and runnable, so a probe run cannot come back unscored.
		var bad []string
		for _, t := range tasks {
			if _, err := practice.Load(filepath.Join(root, "practice-problems", t.Problem+".json")); err != nil {
				bad = append(bad, t.Problem)
			}
		}
		it := CheckItem{Name: "practice problems", OK: len(bad) == 0,
			Detail: fmt.Sprintf("%d/%d present", len(tasks)-len(bad), len(tasks))}
		if len(bad) > 0 {
			it.Detail += "; missing " + strings.Join(bad, ", ")
			it.Fix = "add or fix practice-problems/<problem>.json"
		}
		items = append(items, it)
	}

	// 4. The Go gate, on by default; --skip-tests turns it off for a quick
	//    adapter-only check.
	if !opts.SkipTests {
		if out, err := goRun(ctx, root, "vet", "./..."); err != nil {
			items = append(items, CheckItem{Name: "go vet", Detail: firstLine(out), Fix: "run `go vet ./...`"})
		} else {
			items = append(items, CheckItem{Name: "go vet", OK: true, Detail: "clean"})
		}
		if out, err := goRun(ctx, root, "test", "-race", "./..."); err != nil {
			items = append(items, CheckItem{Name: "go test -race", Detail: firstLine(out), Fix: "run `go test -race ./...`"})
		} else {
			items = append(items, CheckItem{Name: "go test -race", OK: true, Detail: "pass"})
		}
	}

	return items
}

func goRun(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
