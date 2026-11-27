package proc

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// Runs the test binary itself as the child, so it works on every platform.
func TestGroupKillAndAlive(t *testing.T) {
	if os.Getenv("PROC_CHILD") == "1" {
		time.Sleep(time.Minute)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestGroupKillAndAlive")
	cmd.Env = append(os.Environ(), "PROC_CHILD=1")
	Group(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if !Alive(pid) {
		t.Fatal("child should be alive")
	}
	if err := Kill(pid); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if Alive(pid) {
		t.Fatal("child should be gone")
	}
}
