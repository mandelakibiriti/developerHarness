//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
)

// Group gives cmd its own process group so its children can be stopped with it.
func Group(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// Terminate asks the group led by pid to exit.
func Terminate(pid int) error { return syscall.Kill(-pid, syscall.SIGTERM) }

// Kill force-stops the group led by pid.
func Kill(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }

// Signal asks a single process (not necessarily a group leader) to shut down.
func Signal(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// ForceKill ends a single process.
func ForceKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

// Alive reports whether pid is a running process.
func Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
