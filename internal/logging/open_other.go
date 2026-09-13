//go:build !windows

package logging

import "os"

// openLogAppend opens the log file for appending.
func openLogAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

// OpenRead opens a log file for reading. On POSIX systems, standard
// os.Open already allows unlinking and renaming of open files.
func OpenRead(path string) (*os.File, error) {
	return os.Open(path)
}
