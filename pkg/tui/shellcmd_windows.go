//go:build windows

package tui

import (
	"os"
	"os/exec"
)

// configureShellProcessGroup on Windows does not set a separate process group.
func configureShellProcessGroup(cmd *exec.Cmd) {}

// killShellProcessGroup kills a process on Windows.
func killShellProcessGroup(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
