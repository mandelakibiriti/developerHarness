package harness

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunCode(t *testing.T) {
	tests := []struct {
		name, lang, code string
		wantOut          string
		wantExit         int
	}{
		{"go ok", "go", "package main\nimport \"fmt\"\nfunc main(){ fmt.Println(\"hi go\") }\n", "hi go", 0},
		{"python ok", "python", "print('hi py')", "hi py", 0},
		{"js ok", "javascript", "console.log('hi js')", "hi js", 0},
		{"python error exit", "python", "import sys; print('boom', file=sys.stderr); sys.exit(3)", "boom", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := exec.LookPath(runners[tt.lang].cmd[0]); err != nil {
				t.Skip(runners[tt.lang].cmd[0] + " not installed")
			}
			res, err := runCode(context.Background(), tt.lang, tt.code, 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(res.Output, tt.wantOut) || res.ExitCode != tt.wantExit {
				t.Fatalf("got exit=%d output=%q", res.ExitCode, res.Output)
			}
		})
	}
}

func TestRunCodeTimeoutAndCap(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	start := time.Now()
	res, err := runCode(context.Background(), "python", "import time\nwhile True:\n  print('x'*1000, flush=True)\n  time.sleep(0.001)", 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || time.Since(start) > 5*time.Second {
		t.Fatalf("expected timeout kill, got %+v after %v", res.TimedOut, time.Since(start))
	}
	if len(res.Output) > runMaxOutput || !res.Truncated {
		t.Fatalf("output not capped: len=%d truncated=%v", len(res.Output), res.Truncated)
	}
}
