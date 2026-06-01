package migrate

import (
	"fmt"
	"os"

	"dreamer/internal/fsutil"
)

// RunFile reads a JSON file, applies pending migrations from reg, and
// writes the result atomically. On schema upgrade the prior file is
// preserved as <path>.v<old>.bak so the user can roll back.
//
// A missing file is not an error — returns a zero Result.
func RunFile(path string, reg Registry) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Name: reg.Name, ToVersion: reg.CurrentVer}, nil
		}
		return Result{}, fmt.Errorf("read %s %q: %w", reg.Name, path, err)
	}

	migrated, result, err := reg.Run(data)
	if err != nil {
		return result, err
	}

	if !result.Migrated() {
		return result, nil
	}

	// Backup the prior file before overwriting.
	if err := backupFile(path, result.FromVersion, data); err != nil {
		return result, fmt.Errorf("backup %s: %w", reg.Name, err)
	}

	if err := fsutil.WriteFileAtomic(path, migrated, fsutil.FilePerms); err != nil {
		return result, fmt.Errorf("write migrated %s %q: %w", reg.Name, path, err)
	}
	return result, nil
}

// backupFile preserves data at <path>.v<version>.bak if the backup
// doesn't already exist. This keeps the recovery artifact stable
// across repeated saves at the same version.
func backupFile(path string, version int, data []byte) error {
	backupPath := fmt.Sprintf("%s.v%d.bak", path, version)
	if _, err := os.Stat(backupPath); err == nil {
		return nil // already exists — don't overwrite
	}
	return fsutil.WriteFileAtomic(backupPath, data, fsutil.FilePerms)
}
