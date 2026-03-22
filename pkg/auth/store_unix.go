//go:build unix

package auth

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

// checkFilePermissions verifies the auth store file is not group/world accessible.
// This prevents loading (and executing !commands from) a file that another user
// could have tampered with. Similar to OpenSSH's private key permission check.
func checkFilePermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat auth store: %w", err)
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		return fmt.Errorf(
			"auth store %s has insecure permissions %04o; must not be accessible by group or others. Fix with: chmod 600 %s",
			path, perm, path,
		)
	}
	return nil
}

// Lock acquires an exclusive file lock on the auth store.
// It respects ctx for cancellation/timeout — if the context expires while
// waiting for the lock, Lock returns with the context's error.
// Callers must call Unlock when done (typically via defer).
// The lock file is adjacent to auth.json (auth.json.lock).
func (s *Store) Lock(ctx context.Context) error {
	lockPath := s.path + ".lock"

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}

	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			s.lockFile = f
			return nil
		} else if err != syscall.EWOULDBLOCK {
			_ = f.Close()
			return fmt.Errorf("acquire lock: %w", err)
		}

		select {
		case <-ctx.Done():
			_ = f.Close()
			return fmt.Errorf("acquire lock: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Unlock releases the file lock acquired by Lock. Safe to call if not locked.
func (s *Store) Unlock() {
	if s.lockFile != nil {
		_ = syscall.Flock(int(s.lockFile.Fd()), syscall.LOCK_UN)
		_ = s.lockFile.Close()
		s.lockFile = nil
	}
}
