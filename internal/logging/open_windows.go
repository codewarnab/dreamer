//go:build windows

package logging

import (
	"os"

	"golang.org/x/sys/windows"
)

// openLogAppend opens the log file for appending with FILE_SHARE_READ,
// FILE_SHARE_WRITE, and FILE_SHARE_DELETE on Windows. Sharing delete is
// essential so that concurrent readers (or log rotators) can rename or
// unlink the file without encountering ERROR_SHARING_VIOLATION.
func openLogAppend(path string) (*os.File, error) {
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// FILE_APPEND_DATA ensures that writes atomically append to the end of the file.
	// FILE_READ_ATTRIBUTES allows os.File.Stat() to query file metadata/size.
	access := uint32(windows.FILE_APPEND_DATA | windows.FILE_READ_ATTRIBUTES | windows.FILE_WRITE_ATTRIBUTES | windows.STANDARD_RIGHTS_WRITE | windows.SYNCHRONIZE)
	shareMode := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	handle, err := windows.CreateFile(
		path16,
		access,
		shareMode,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// OpenRead opens a log file for reading with FILE_SHARE_READ,
// FILE_SHARE_WRITE, and FILE_SHARE_DELETE on Windows. This prevents
// log tail readers from locking the log file against size-based rotation.
func OpenRead(path string) (*os.File, error) {
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	shareMode := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	handle, err := windows.CreateFile(
		path16,
		windows.GENERIC_READ,
		shareMode,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}
