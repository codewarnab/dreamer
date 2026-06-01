// Package fsutil is a test stub for dreamer/internal/fsutil.
package fsutil

// AcquireLock acquires a file lock at path and returns a release function.
func AcquireLock(path string) (release func(), err error) {
	return func() {}, nil
}
