//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
)

// configureProcessGroup on Windows does not set a separate process group.
// Windows handles process termination differently - child processes are
// automatically associated with the parent's job object.
func configureProcessGroup(cmd *exec.Cmd) {
	// No action needed on Windows
}

// killProcessGroup kills a process on Windows.
// On Windows, we kill the process directly.
func killProcessGroup(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	return proc.Kill()
}
