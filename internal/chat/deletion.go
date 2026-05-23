package chat

import (
	"fmt"
	"os"
)

// deleteSourceFile unlinks a chat source whose Path is a single file on disk.
// Missing files are treated as success so retries are idempotent.
func deleteSourceFile(path string) error {
	if path == "" {
		return fmt.Errorf("chat source path is empty")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove chat source %q: %w", path, err)
	}
	return nil
}

// statSourceSize returns the byte size of a file-backed chat source via stat.
// Missing files return (0, nil) so they sort to the bottom without breaking
// the chats list.
func statSourceSize(path string) (int64, error) {
	if path == "" {
		return 0, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat chat source %q: %w", path, err)
	}
	return info.Size(), nil
}
