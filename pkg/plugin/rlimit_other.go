//go:build !linux

package plugin

import "fmt"

// setMemoryLimit is a no-op on non-Linux platforms.
func setMemoryLimit(pid int, limitMB int64) error {
	return fmt.Errorf("memory limits not supported on this platform")
}
