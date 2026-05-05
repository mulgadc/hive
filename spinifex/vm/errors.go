package vm

import "errors"

// ErrInstanceNotFound is returned by manager methods that look up an
// instance by id when no entry exists in the running map.
var ErrInstanceNotFound = errors.New("instance not found")

// ErrInvalidTransition is returned when a lifecycle method cannot run
// because the instance's current state does not permit the target
// transition (e.g. Stop on an already-stopped instance).
var ErrInvalidTransition = errors.New("invalid state transition")
