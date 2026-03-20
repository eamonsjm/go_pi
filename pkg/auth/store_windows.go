//go:build windows

package auth

import "sync"

// For Windows, use an in-process mutex instead of file locking.
// This protects against concurrent access within the same process.
var storeMutex sync.Mutex

// Lock acquires a lock on the auth store.
// On Windows, this uses an in-process mutex instead of file locking.
// Callers must call Unlock when done (typically via defer).
func (s *Store) Lock() error {
	storeMutex.Lock()
	return nil
}

// Unlock releases the lock acquired by Lock. Safe to call if not locked.
func (s *Store) Unlock() {
	storeMutex.Unlock()
}
