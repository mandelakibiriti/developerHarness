//go:build windows

package proc

import (
	"os/exec"
	"strconv"
	"syscall"
)

// Group starts cmd in a new process group, so Ctrl+C in the harness console is
// not also delivered to the tools it supervises.
func Group(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// Windows has no SIGTERM for another process, so every stop ends the process
// tree with taskkill.
func tree(pid int) error {
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}

// Terminate stops the process tree under pid.
func Terminate(pid int) error { return tree(pid) }

// Kill stops the process tree under pid.
func Kill(pid int) error { return tree(pid) }

// Signal stops pid and its children.
func Signal(pid int) error { return tree(pid) }

// ForceKill stops pid and its children.
func ForceKill(pid int) error { return tree(pid) }

// Alive reports whether pid is a running process.
func Alive(pid int) bool {
	const queryLimitedInformation, stillActive = 0x1000, 259
	h, err := syscall.OpenProcess(queryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	return syscall.GetExitCodeProcess(h, &code) == nil && code == stillActive
}
