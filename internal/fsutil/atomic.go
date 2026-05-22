package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DirPerms is the default permission for newly created directories.
	DirPerms = 0o755
	// FilePerms is the default permission for newly created files.
	FilePerms = 0o644
)

// WriteFileAtomic writes contentBytes to path via a sibling temp file + rename, with
// fsync on both the temp file body and the parent directory so the new entry
// is durable across power loss (B7). A crash mid-write leaves the previous
// target (if any) intact rather than a half-written file. The parent
// directory must already exist.
func WriteFileAtomic(path string, contentBytes []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open atomic temp %q: %w", tmp, err)
	}
	if _, err := f.Write(contentBytes); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write atomic temp %q: %w", tmp, err)
	}
	// Fsync the file body before rename so the rename doesn't make an
	// empty-content file visible after a crash.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync atomic temp %q: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close atomic temp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename atomic temp %q -> %q: %w", tmp, path, err)
	}
	// Fsync parent dir to persist the rename itself. Best-effort: some
	// filesystems (e.g. tmpfs, NFS) reject this; we don't fail the write.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
