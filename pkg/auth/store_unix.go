//go:build unix

package auth

import (
	"fmt"
	"os"
	"syscall"
)

// Lock acquires an exclusive file lock on the auth store.
// Callers must call Unlock when done (typically via defer).
// The lock file is adjacent to auth.json (auth.json.lock).
func (s *Store) Lock() error {
	lockPath := s.path + ".lock"

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return fmt.Errorf("acquire lock: %w", err)
	}

	s.lockFile = f
	return nil
}

// Unlock releases the file lock acquired by Lock. Safe to call if not locked.
func (s *Store) Unlock() {
	if s.lockFile != nil {
		_ = syscall.Flock(int(s.lockFile.Fd()), syscall.LOCK_UN)
		_ = s.lockFile.Close()
		s.lockFile = nil
	}
}
