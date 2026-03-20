//go:build unix

package tools

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup configures the process to use its own process group (Unix-specific).
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kills the process group (Unix-specific).
// On Unix, we kill the entire process group using the negative PID.
func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
