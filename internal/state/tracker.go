package state

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/gitutil"
	"dreamer/internal/logging"
	"dreamer/internal/migrate"
)

const (
	stateFile = "state.json"

	// StateVersion bumps when the on-disk shape of state.json or the
	// derivation of a stored value changes such that a v(N-1) file cannot
	// be loaded as-is. v2 introduces length-prefixed ChatCacheKey (B21),
	// which makes prior ChatHashes entries hash-incompatible.
	StateVersion = 2
)

// stateMigrations is the ordered migration registry for state.json.
// Each migration receives raw JSON bytes from the prior version and
// returns bytes for the next version. Add new migrations here when
// bumping StateVersion — no if-chains to edit.
var stateMigrations = migrate.Registry{
	Name:       "state.json",
	CurrentVer: StateVersion,
	Migrations: []migrate.Migration{
		{
			FromVersion: 0,
			ToVersion:   1,
			Description: "add version field to legacy files",
			Migrate: func(data []byte) ([]byte, error) {
				return migrate.SetVersion(data, 1)
			},
		},
		{
			FromVersion: 1,
			ToVersion:   2,
			Description: "length-prefixed ChatCacheKey; clear ChatHashes",
			Migrate: func(data []byte) ([]byte, error) {
				var m map[string]json.RawMessage
				if err := json.Unmarshal(data, &m); err != nil {
					return nil, fmt.Errorf("unmarshal: %w", err)
				}
				// ChatCacheKey is now length-prefixed, so prior entries
				// no longer compare equal to freshly-computed keys.
				m["chat_hashes"] = []byte("{}")
				m["version"] = []byte("2")
				return json.Marshal(m)
			},
		},
	},
}

// ProviderUsage records aggregate counters per provider id.
type ProviderUsage struct {
	Runs        int64 `json:"runs"`
	TotalTokens int64 `json:"total_tokens"`
	Timeouts    int64 `json:"timeouts,omitempty"`
	Failures    int64 `json:"failures,omitempty"`

	// LastSuccessUTC: most recent successful provider call.
	LastSuccessUTC time.Time `json:"last_success_utc,omitempty"`

	// LastError: most recent error message (truncated). Cleared on next success.
	LastError string `json:"last_error,omitempty"`
}

// State is the per-project persistent state document.
type State struct {
	Version       int                      `json:"version"`
	LastRunUTC    time.Time                `json:"last_run_utc,omitempty"`
	RepoHeadSHA   string                   `json:"repo_head_sha,omitempty"`
	ChatHashes    map[string]string        `json:"chat_hashes"`
	FindingHashes []string                 `json:"finding_hashes"`
	ProviderUsage map[string]ProviderUsage `json:"provider_usage"`

	// Optional misc counters (claude_messages_kept, claude_messages_dropped, ...).
	UsageStats map[string]int64 `json:"usage_stats,omitempty"`

	// LastRunPerCategory: most recent successful completion per rule category.
	// Stale entries flag a timing-out or erroring rule.
	LastRunPerCategory map[string]time.Time `json:"last_run_per_category,omitempty"`

	// Findings is the v1.5 per-finding lifecycle map. Open findings are
	// absent (zero-value semantics).
	Findings map[string]FindingState `json:"findings,omitempty"`

	// CachedPhase1 holds Phase 1 mistakes from a prior run where Phase 1
	// succeeded but the full pipeline did not complete (e.g. Phase 2 failed).
	// On the next run, if the cache key matches, Phase 1 is skipped and these
	// mistakes are replayed into Phase 2 directly. Nil means no cache.
	CachedPhase1 *CachedPhase1Result `json:"cached_phase1,omitempty"`
}

// CachedPhase1Result stores Phase 1 output for replay on the next run.
type CachedPhase1Result struct {
	// Mistakes is the Phase 1 output keyed by category ID string.
	Mistakes map[string][]CachedMistake `json:"mistakes"`
	// CacheKey is a deterministic hash of the transcript + config that
	// produced these mistakes. Mismatches on the next run invalidate the cache.
	CacheKey string `json:"cache_key"`
	// Timestamp records when the cache was written (for observability).
	Timestamp time.Time `json:"timestamp"`
}

// CachedMistake mirrors analyzer.Mistake but uses plain string for Category
// to avoid cross-package serialization coupling.
type CachedMistake struct {
	Category        string  `json:"category"`
	Summary         string  `json:"summary"`
	EvidenceExcerpt string  `json:"evidence_excerpt"`
	Confidence      float64 `json:"confidence"`
}

// PathForProject returns the per-project state.json path under outputRoot.
// If outputRoot is empty, falls back to <UserConfigDir>/dreamer.
func PathForProject(outputRoot string, projectName string) (string, error) {
	if err := config.ValidateProjectName(projectName); err != nil {
		return "", err
	}

	name := strings.TrimSpace(projectName)
	root := strings.TrimSpace(outputRoot)
	if root == "" {
		cfgRoot, err := config.UserConfigRoot()
		if err != nil {
			return "", fmt.Errorf("resolve user config dir: %w", err)
		}
		root = cfgRoot
	}
	return filepath.Join(root, name, stateFile), nil
}

// LoadResult bundles the loaded State with metadata about any migration
// that ran. PriorVersion == 0 means either no file existed or the prior
// file was pre-versioning legacy; CurrentVersion is what the in-memory
// State now claims. Migrated reports whether Load mutated the on-disk
// schema (so the caller can log it / produce a user-visible notice).
type LoadResult struct {
	State          *State
	PriorVersion   int
	CurrentVersion int
	Migrated       bool
}

// Load reads the per-project state. A missing file returns a default state
// (non-nil, version=current, empty maps). The version field is gated (B12/
// B27): an explicit `version=0` is refused as suspect, a newer version is
// refused to avoid downgrade-on-write, and a missing version field is
// treated as a pre-versioning legacy file and upgraded to the current
// schema in memory. Callers that need the migration signal should use
// LoadWithResult.
func Load(outputRoot, projectName string) (*State, error) {
	r, err := LoadWithResult(outputRoot, projectName)
	if err != nil {
		return nil, err
	}
	return r.State, nil
}

// LoadWithResult is Load + a migration-info return value.
func LoadWithResult(outputRoot, projectName string) (LoadResult, error) {
	path, err := PathForProject(outputRoot, projectName)
	if err != nil {
		return LoadResult{}, err
	}
	stateBytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LoadResult{State: defaultState(), CurrentVersion: StateVersion}, nil
		}
		return LoadResult{}, fmt.Errorf("read state file %q: %w", path, err)
	}

	migrated, result, err := stateMigrations.Run(stateBytes)
	if err != nil {
		return LoadResult{}, fmt.Errorf("migrate state file %q: %w", path, err)
	}

	var current State
	if err := json.Unmarshal(migrated, &current); err != nil {
		return LoadResult{}, fmt.Errorf("unmarshal state file %q: %w", path, err)
	}
	normalizeState(&current)
	return LoadResult{
		State:          &current,
		PriorVersion:   result.FromVersion,
		CurrentVersion: StateVersion,
		Migrated:       result.Migrated(),
	}, nil
}

// Save writes the per-project state atomically via temp file + os.Rename
// (B3). On a *schema upgrade* (prior file's version < StateVersion) the
// prior file is preserved at <path>.v<prior-version>.bak so the user can
// roll back; same-version saves do NOT overwrite the backup so the recovery
// artifact is stable (B12).
func Save(outputRoot, projectName string, state *State) error {
	if state == nil {
		return fmt.Errorf("state is required")
	}
	path, err := PathForProject(outputRoot, projectName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), fsutil.DirPerms); err != nil {
		return fmt.Errorf("create state directory %q: %w", filepath.Dir(path), err)
	}

	if priorData, readErr := os.ReadFile(path); readErr == nil {
		priorVersion, hasVersion, _ := migrate.PeekVersion(priorData)
		if !hasVersion {
			priorVersion = 0
		}
		if priorVersion < StateVersion {
			backupPath := fmt.Sprintf("%s.v%d.bak", path, priorVersion)
			if _, err := os.Stat(backupPath); os.IsNotExist(err) {
				if writeErr := fsutil.WriteFileAtomic(backupPath, priorData, fsutil.FilePerms); writeErr != nil {
					return fmt.Errorf("write schema-upgrade backup %q: %w", backupPath, writeErr)
				}
			}
		}
	}

	stateCopy := *state
	if stateCopy.Version == 0 {
		stateCopy.Version = StateVersion
	}
	normalizeState(&stateCopy)
	stateBytes, err := json.MarshalIndent(&stateCopy, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state for project %q: %w", projectName, err)
	}

	if err := fsutil.WriteFileAtomic(path, stateBytes, fsutil.FilePerms); err != nil {
		return fmt.Errorf("save state for project %q: %w", projectName, err)
	}
	return nil
}

func defaultState() *State {
	return &State{
		Version:            StateVersion,
		ChatHashes:         map[string]string{},
		FindingHashes:      []string{},
		ProviderUsage:      map[string]ProviderUsage{},
		UsageStats:         map[string]int64{},
		LastRunPerCategory: map[string]time.Time{},
		Findings:           map[string]FindingState{},
	}
}

func normalizeState(s *State) {
	if s.ChatHashes == nil {
		s.ChatHashes = map[string]string{}
	}
	if s.FindingHashes == nil {
		s.FindingHashes = []string{}
	}
	if s.ProviderUsage == nil {
		s.ProviderUsage = map[string]ProviderUsage{}
	}
	if s.UsageStats == nil {
		s.UsageStats = map[string]int64{}
	}
	if s.LastRunPerCategory == nil {
		s.LastRunPerCategory = map[string]time.Time{}
	}
	if s.Findings == nil {
		s.Findings = map[string]FindingState{}
	}
}

// maxErrorLen caps persisted error strings (runes).
const maxErrorLen = 500

// TruncateError clamps to maxErrorLen runes; appends '…' on truncation.
func TruncateError(msg string) string {
	if msg == "" {
		return ""
	}
	runes := []rune(msg)
	if len(runes) <= maxErrorLen {
		return msg
	}
	return string(runes[:maxErrorLen]) + "…"
}

// HashFile returns the hex-encoded sha256 of a file's contents.
func HashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash %q: %w", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// ChatCacheKey computes the cache key for one chat file (spec §12).
// Each field is length-prefixed before hashing so a NUL byte inside any
// field cannot shift the field boundary and forge a collision (B21).
func ChatCacheKey(path, fileHash, repoHeadSHA string) string {
	hasher := sha256.New()
	writeLenPrefixed := func(s string) {
		var lenBuf [8]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(s)))
		hasher.Write(lenBuf[:])
		hasher.Write([]byte(s))
	}
	writeLenPrefixed(path)
	writeLenPrefixed(fileHash)
	writeLenPrefixed(repoHeadSHA)
	return hex.EncodeToString(hasher.Sum(nil))
}

// RepoHeadSHA returns the git HEAD sha for a working directory.
//
// Non-git repositories and git command failures return an empty string because
// repository state is a cache hint, not a hard requirement for analysis. When a
// logger is provided, failures are recorded at debug level for troubleshooting.
func RepoHeadSHA(workingDirectory string, loggers ...*logging.Logger) string {
	workingDirectoryPath := strings.TrimSpace(workingDirectory)
	if workingDirectoryPath == "" {
		return ""
	}
	cmd, cancel := gitutil.Command(context.Background(), "-C", workingDirectoryPath, "rev-parse", "HEAD")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		if len(loggers) > 0 && loggers[0] != nil {
			loggers[0].Debug("repo head SHA failed", logging.Any("wd", workingDirectoryPath), logging.Any("err", err))
		}
		return ""
	}
	return strings.TrimSpace(string(out))
}
