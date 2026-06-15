package handlers

import "dreamer/internal/state"

// ProjectLock is an alias for state.ProjectLock.
//
// ProjectLock was originally defined here (in the handlers package) because
// it was only needed by web lifecycle handlers.  It has been moved to the
// state package so the pipeline can also import it without creating an import
// cycle (pipeline → web/handlers would be circular).
//
// The alias keeps all existing handler code compiling without modification:
// handlers.NewProjectLock(), handlers.ProjectLock, and deps.StateLock all
// continue to work exactly as before.
type ProjectLock = state.ProjectLock

// NewProjectLock creates a ProjectLock ready for use.
// Delegates to state.NewProjectLock so callers in this package need not
// change their import list.
func NewProjectLock() *ProjectLock {
	return state.NewProjectLock()
}
