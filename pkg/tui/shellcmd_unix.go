//go:build unix

package tui

import (
	"os/exec"
	"syscall"
)

// configureShellProcessGroup sets up a separate process group for shell commands.
func configureShellProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killShellProcessGroup kills the entire process group.
func killShellProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
