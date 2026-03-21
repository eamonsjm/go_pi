//go:build windows

package auth

// checkFilePermissions is a no-op on Windows. Unix filesystem permissions
// do not apply; Windows uses ACLs which are set correctly by the OS when
// writing to the user's home directory.
func checkFilePermissions(_ string) error {
	return nil
}

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
