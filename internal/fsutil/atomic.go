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
	// SecretPerms is the permission for files containing secrets (tokens, keys).
	// Only the owner can read or write.
	SecretPerms = 0o600
)

// WriteFileAtomic writes contentBytes to path via a unique temp file + rename,
// with fsync on both the temp file body and the parent directory so the new
// entry is durable across power loss (B7). A crash mid-write leaves the
// previous target (if any) intact rather than a half-written file. The parent
// directory must already exist.
func WriteFileAtomic(path string, contentBytes []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	file, err := os.CreateTemp(dir, base+".tmp.*")
	if err != nil {
		return fmt.Errorf("open atomic temp in %q: %w", dir, err)
	}
	tempPath := file.Name()
	if err := file.Chmod(perm); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("chmod atomic temp %q: %w", tempPath, err)
	}
	if _, err := file.Write(contentBytes); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("write atomic temp %q: %w", tempPath, err)
	}
	// Fsync the file body before rename so the rename doesn't make an
	// empty-content file visible after a crash.
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("fsync atomic temp %q: %w", tempPath, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close atomic temp %q: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename atomic temp %q -> %q: %w", tempPath, path, err)
	}
	// Fsync parent dir to persist the rename itself. Best-effort: some
	// filesystems (e.g. tmpfs, NFS) reject this; we don't fail the write.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		//astcheck:ignore[discardederr] best-effort: see comment above
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
