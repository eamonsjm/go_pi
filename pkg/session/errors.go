package session

import "errors"

// ErrNoActiveSession is returned when an operation requires an active session
// but none has been created or loaded.
var ErrNoActiveSession = errors.New("no active session")

// ErrEntryNotFound is returned when a session entry cannot be found by ID.
// Use errors.Is to check for this sentinel. Use errors.As with
// *EntryNotFoundError to retrieve the missing entry ID.
var ErrEntryNotFound = errors.New("entry not found in session")

// EntryNotFoundError provides the ID of the missing entry.
type EntryNotFoundError struct {
	EntryID string
}

func (e *EntryNotFoundError) Error() string {
	return "entry " + e.EntryID + " not found in session"
}

func (e *EntryNotFoundError) Is(target error) bool {
	return target == ErrEntryNotFound
}
