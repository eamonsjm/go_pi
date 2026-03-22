//go:build windows

package auth

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	// lockfileFailImmediately causes LockFileEx to return immediately if the
	// lock cannot be acquired, rather than blocking.
	lockfileFailImmediately = 0x00000001
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
		ol := new(windows.Overlapped)
		err := windows.LockFileEx(
			windows.Handle(f.Fd()),
			lockfileExclusiveLock|lockfileFailImmediately,
			0, // reserved
			1, // lock 1 byte
			0, // high word
			ol,
		)
		if err == nil {
			s.lockFile = f
			return nil
		}
		if err != windows.ERROR_LOCK_VIOLATION {
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
