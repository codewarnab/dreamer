// Package migrate provides a version-gated migration framework for persisted
// JSON files. Each file type registers a Registry with ordered migration steps;
// the framework handles version detection, ordered application, downgrade
// rejection, and backup-before-upgrade.
//
// Migrations are pure functions ([]byte → []byte) — no I/O, no side effects.
// The file-level RunFile helper handles read/backup/write.
package migrate

import (
	"encoding/json"
	"fmt"
)

// Migration is a single schema upgrade step.
type Migration struct {
	FromVersion int
	ToVersion   int
	// Migrate transforms raw JSON bytes from FromVersion to ToVersion.
	// Must be pure: no I/O, no side effects beyond the returned bytes.
	Migrate func(data []byte) ([]byte, error)
	// Description is a human-readable one-liner for logging.
	Description string
}

// Registry holds ordered migrations for a single persisted file type.
// Migrations must be sorted by FromVersion ascending with no gaps.
type Registry struct {
	// Name identifies the file type (e.g. "state.json"). Used in error messages.
	Name string
	// CurrentVer is the version this binary writes.
	CurrentVer int
	// Migrations is the ordered list of upgrade steps.
	Migrations []Migration
}

// Result captures the outcome of a migration run.
type Result struct {
	Name         string
	FromVersion  int
	ToVersion    int
	StepsApplied int
}

// Migrated reports whether any migration steps were applied.
func (r Result) Migrated() bool { return r.StepsApplied > 0 }

// Run applies all pending migrations in order.
//
// Behavior:
//   - data with version == CurrentVer → returns data unchanged (no-op)
//   - data with version > CurrentVer → error (downgrade protection)
//   - data with version == 0 and no "version" key → treated as pre-versioning legacy, migrated from version 0
//   - data with explicit version == 0 → error (suspect truncation)
//   - data with version < CurrentVer → applies migrations in order
func (r *Registry) Run(data []byte) ([]byte, Result, error) {
	result := Result{Name: r.Name, ToVersion: r.CurrentVer}

	fileVer, hasVersion, err := PeekVersion(data)
	if err != nil {
		return nil, result, fmt.Errorf("peek version in %s: %w", r.Name, err)
	}

	if hasVersion && fileVer == 0 {
		return nil, result, fmt.Errorf("%s has explicit version=0; refusing to load (suspect truncation)", r.Name)
	}

	if hasVersion && fileVer > r.CurrentVer {
		return nil, result, fmt.Errorf("%s has version %d but this binary supports up to %d; refusing to load (downgrade risk)", r.Name, fileVer, r.CurrentVer)
	}

	if !hasVersion {
		fileVer = 0
	}

	result.FromVersion = fileVer

	if fileVer == r.CurrentVer {
		return data, result, nil
	}

	// Apply migrations in order.
	current := data
	for _, m := range r.Migrations {
		if m.FromVersion < fileVer {
			continue
		}
		if m.FromVersion > fileVer {
			break
		}
		migrated, err := m.Migrate(current)
		if err != nil {
			return nil, result, fmt.Errorf("%s migration v%d→v%d (%s): %w", r.Name, m.FromVersion, m.ToVersion, m.Description, err)
		}
		current = migrated
		result.StepsApplied++
		fileVer = m.ToVersion
	}

	if fileVer != r.CurrentVer {
		return nil, result, fmt.Errorf("%s ended at version %d after migrations, expected %d; migration chain is incomplete", r.Name, fileVer, r.CurrentVer)
	}

	return current, result, nil
}

// PeekVersion extracts the "version" field from raw JSON bytes.
// Returns (version, true, nil) if the field exists, (0, false, nil) if absent.
func PeekVersion(data []byte) (int, bool, error) {
	var peek struct {
		Version json.RawMessage `json:"version"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return 0, false, fmt.Errorf("unmarshal: %w", err)
	}
	if len(peek.Version) == 0 {
		return 0, false, nil
	}
	// Could be null, string, etc. — only accept a numeric value.
	var v int
	if err := json.Unmarshal(peek.Version, &v); err != nil {
		return 0, false, fmt.Errorf("unmarshal version field: %w", err)
	}
	return v, true, nil
}

// SetVersion replaces the "version" field in raw JSON bytes.
// If the field doesn't exist, it's added at the top level.
func SetVersion(data []byte, version int) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	verBytes, err := json.Marshal(version)
	if err != nil {
		return nil, fmt.Errorf("marshal version: %w", err)
	}
	m["version"] = verBytes
	return json.Marshal(m)
}
