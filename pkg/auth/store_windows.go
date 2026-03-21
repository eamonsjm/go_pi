//go:build windows

package auth

// Lock acquires a lock on the auth store.
// On Windows, this uses a per-instance mutex instead of file locking.
// Callers must call Unlock when done (typically via defer).
func (s *Store) Lock() error {
	s.mu.Lock()
	return nil
}

// Unlock releases the lock acquired by Lock. Safe to call if not locked.
func (s *Store) Unlock() {
	s.mu.Unlock()
}
