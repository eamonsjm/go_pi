//go:build windows

package auth

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

const (
	// lockfileExclusiveLock requests an exclusive (write) lock.
	lockfileExclusiveLock = 0x00000002
)

// checkFilePermissions is a no-op on Windows. Unix filesystem permissions
// do not apply; Windows uses ACLs which are set correctly by the OS when
// writing to the user's home directory.
func checkFilePermissions(_ string) error {
	return nil
}

// Lock acquires an exclusive file lock on the auth store.
// Callers must call Unlock when done (typically via defer).
// The lock file is adjacent to auth.json (auth.json.lock).
func (s *Store) Lock() error {
	lockPath := s.path + ".lock"

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}

	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		lockfileExclusiveLock,
		0, // reserved
		1, // lock 1 byte
		0, // high word
		ol,
	); err != nil {
		_ = f.Close()
		return fmt.Errorf("acquire lock: %w", err)
	}

	s.lockFile = f
	return nil
}

// Unlock releases the file lock acquired by Lock. Safe to call if not locked.
func (s *Store) Unlock() {
	if s.lockFile != nil {
		ol := new(windows.Overlapped)
		_ = windows.UnlockFileEx(
			windows.Handle(s.lockFile.Fd()),
			0, // reserved
			1, // unlock 1 byte
			0, // high word
			ol,
		)
		_ = s.lockFile.Close()
		s.lockFile = nil
	}
}
