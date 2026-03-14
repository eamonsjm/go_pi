package plugin

import (
	"golang.org/x/sys/unix"
)

// setMemoryLimit sets RLIMIT_AS (virtual memory) on the given process.
func setMemoryLimit(pid int, limitMB int64) error {
	limitBytes := uint64(limitMB) * 1024 * 1024
	rlim := unix.Rlimit{
		Cur: limitBytes,
		Max: limitBytes,
	}
	return unix.Prlimit(pid, unix.RLIMIT_AS, &rlim, nil)
}
