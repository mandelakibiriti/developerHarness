package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devex/harness/internal/config"
)

func TestStartStop(t *testing.T) {
	tool := config.Tool{ID: "sleep", Command: []string{"sleep", "30"}}
	dir := t.TempDir()

	p, err := Start(tool, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Running() {
		t.Fatal("expected running")
	}
	if _, err := os.Stat(filepath.Join(dir, "sleep.log")); err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	if err := p.Stop(2 * time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if p.Running() {
		t.Fatal("expected stopped")
	}
}
