// Package proc holds the few process operations that differ between Unix and
// Windows: starting a child with its own group, and stopping that whole tree.
package proc

import "runtime"

// Python is the interpreter's usual name on this platform.
func Python() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}
