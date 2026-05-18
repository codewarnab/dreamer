package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path via a sibling temp file + rename, then
// fsyncs the parent directory so the new entry is durable across power loss
// (B7). A crash mid-write leaves the previous target (if any) intact rather
// than a half-written file. The parent directory must already exist.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write atomic temp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename atomic temp %q -> %q: %w", tmp, path, err)
	}
	// Fsync parent dir to persist the rename across power loss. Best-effort:
	// some filesystems (e.g. tmpfs, NFS) reject this; we don't fail the write.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
