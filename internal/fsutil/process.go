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
	pid, _, err := readLockMetadata(path)
	return pid, err
}

// ReadLockMetadata reads both the PID and the executable path from the
// given lock file. Callers should verify the executable identity via
// ExecPathsMatch against ProcessExecutable(pid) before acting on the PID,
// to guard against PID reuse.
func ReadLockMetadata(path string) (pid int, execPath string, err error) {
	return readLockMetadata(path)
}

// ProcessExecutable returns the executable path of the live process with
// the given PID. Returns ("", false) if the process is not running or
// the path cannot be determined.
func ProcessExecutable(pid int) (string, bool) {
	return processExecutable(pid)
}
