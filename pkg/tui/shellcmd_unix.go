//go:build unix

package tui

import (
	"fmt"
	"os/exec"
	"syscall"
)

// configureShellProcessGroup sets up a separate process group for shell commands.
func configureShellProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killShellProcessGroup kills the entire process group.
func killShellProcessGroup(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d: refusing to kill process group", pid)
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}
