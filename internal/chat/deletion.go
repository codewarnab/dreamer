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
