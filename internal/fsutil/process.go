package fsutil

// IsProcessAlive reports whether the process with the given PID is still
// running. Used by the job queue recovery logic to detect stale jobs left
// behind by a daemon crash.
func IsProcessAlive(pid int) bool {
	return isProcessAlive(pid)
}

// ReadLockPID reads the PID from the given lock file. Used by the job
// queue recovery logic to check whether a prior daemon process is alive.
func ReadLockPID(path string) (int, error) {
	return readLockPID(path)
}
