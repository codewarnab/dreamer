package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	configDirName = "dreamer"
	stateFile     = "state.json"
	dirPerms      = 0o755
	statePerms    = 0o644

	StateVersion = 1
)

// ProviderUsage records aggregate counters per provider id.
type ProviderUsage struct {
	Runs        int64 `json:"runs"`
	TotalTokens int64 `json:"total_tokens"`
	Timeouts    int64 `json:"timeouts,omitempty"`
	Failures    int64 `json:"failures,omitempty"`
}

// State is the per-project persistent state document (spec §12).
type State struct {
	Version       int                      `json:"version"`
	LastRunUTC    time.Time                `json:"last_run_utc,omitempty"`
	RepoHeadSHA   string                   `json:"repo_head_sha,omitempty"`
	ChatHashes    map[string]string        `json:"chat_hashes"`
	FindingHashes []string                 `json:"finding_hashes"`
	ProviderUsage map[string]ProviderUsage `json:"provider_usage"`

	// Optional misc counters (claude_messages_kept, claude_messages_dropped, ...).
	UsageStats map[string]int64 `json:"usage_stats,omitempty"`
}

// PathForProject returns the per-project state.json path under outputRoot.
// If outputRoot is empty, falls back to <UserConfigDir>/dreamer.
func PathForProject(outputRoot string, projectName string) (string, error) {
	name := strings.TrimSpace(projectName)
	if name == "" {
		return "", fmt.Errorf("project name is required")
	}
	if strings.ContainsAny(name, `\/`) {
		return "", fmt.Errorf("project name contains invalid path separator: %q", projectName)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("project name is invalid: %q", projectName)
	}

	root := strings.TrimSpace(outputRoot)
	if root == "" {
		cfgDir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve user config dir: %w", err)
		}
		root = filepath.Join(cfgDir, configDirName)
	}
	return filepath.Join(root, name, stateFile), nil
}

// Load reads the per-project state. A missing file returns a default state
// (non-nil, version=1, empty maps).
func Load(outputRoot, projectName string) (*State, error) {
	path, err := PathForProject(outputRoot, projectName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultState(), nil
		}
		return nil, fmt.Errorf("read state file %q: %w", path, err)
	}
	var current State
	if err := json.Unmarshal(data, &current); err != nil {
		return nil, fmt.Errorf("unmarshal state file %q: %w", path, err)
	}
	normalizeState(&current)
	return &current, nil
}

// Save writes the per-project state.
func Save(outputRoot, projectName string, state *State) error {
	if state == nil {
		return fmt.Errorf("state is required")
	}
	path, err := PathForProject(outputRoot, projectName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerms); err != nil {
		return fmt.Errorf("create state directory %q: %w", filepath.Dir(path), err)
	}
	dup := *state
	normalizeState(&dup)
	data, err := json.MarshalIndent(&dup, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state for project %q: %w", projectName, err)
	}
	if err := os.WriteFile(path, data, statePerms); err != nil {
		return fmt.Errorf("write state file %q: %w", path, err)
	}
	return nil
}

// LoadState is a legacy wrapper kept for the existing daemon/analyze loop;
// it loads from the default output root.
func LoadState(projectName string) (*State, error) {
	return Load("", projectName)
}

// SaveState is a legacy wrapper kept for the existing daemon/analyze loop.
func SaveState(projectName string, state *State) error {
	return Save("", projectName, state)
}

func defaultState() *State {
	return &State{
		Version:       StateVersion,
		ChatHashes:    map[string]string{},
		FindingHashes: []string{},
		ProviderUsage: map[string]ProviderUsage{},
		UsageStats:    map[string]int64{},
	}
}

func normalizeState(s *State) {
	if s.Version == 0 {
		s.Version = StateVersion
	}
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
// path/fileHash/repoHeadSHA are concatenated then sha256'd.
func ChatCacheKey(path, fileHash, repoHeadSHA string) string {
	hasher := sha256.New()
	hasher.Write([]byte(path))
	hasher.Write([]byte{0})
	hasher.Write([]byte(fileHash))
	hasher.Write([]byte{0})
	hasher.Write([]byte(repoHeadSHA))
	return hex.EncodeToString(hasher.Sum(nil))
}

// RepoHeadSHA returns the git HEAD sha for a working directory. Non-git
// repositories (or git failures) return "" with no error.
func RepoHeadSHA(workingDirectory string) string {
	wd := strings.TrimSpace(workingDirectory)
	if wd == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", wd, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
