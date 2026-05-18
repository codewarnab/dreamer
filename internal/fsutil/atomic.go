package fsutil

import (
	"fmt"
	"os"
)

// WriteFileAtomic writes data to path via a sibling temp file + rename. A
// crash mid-write leaves the previous target (if any) intact rather than a
// half-written file. The parent directory must already exist.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write atomic temp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename atomic temp %q -> %q: %w", tmp, path, err)
	}
	return nil
}
