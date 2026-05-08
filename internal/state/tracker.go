package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	rootDirName = ".dreamer"
	stateFile   = "state.json"
	dirPerms    = 0o755
	statePerms  = 0o644
)

type State struct {
	LastRun            time.Time        `json:"last_run,omitempty"`
	AnalyzedChatIDs    []string         `json:"analyzed_chat_ids"`
	ResumableSessionID string           `json:"resumable_session_id,omitempty"`
	UsageStats         map[string]int64 `json:"usage_stats"`
}

func LoadState(projectName string) (*State, error) {
	statePath, err := statePathForProject(projectName)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultState(), nil
		}
		return nil, fmt.Errorf("read state file %q: %w", statePath, err)
	}

	var current State
	if err := json.Unmarshal(data, &current); err != nil {
		return nil, fmt.Errorf("unmarshal state file %q: %w", statePath, err)
	}

	normalizeState(&current)
	return &current, nil
}

func SaveState(projectName string, state *State) error {
	if state == nil {
		return fmt.Errorf("state is required")
	}

	statePath, err := statePathForProject(projectName)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(statePath), dirPerms); err != nil {
		return fmt.Errorf("create state directory %q: %w", filepath.Dir(statePath), err)
	}

	copyState := *state
	normalizeState(&copyState)

	data, err := json.MarshalIndent(&copyState, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state for project %q: %w", projectName, err)
	}

	if err := os.WriteFile(statePath, data, statePerms); err != nil {
		return fmt.Errorf("write state file %q: %w", statePath, err)
	}

	return nil
}

func statePathForProject(projectName string) (string, error) {
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

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}

	return filepath.Join(home, rootDirName, name, stateFile), nil
}

func defaultState() *State {
	return &State{
		AnalyzedChatIDs: []string{},
		UsageStats:      map[string]int64{},
	}
}

func normalizeState(state *State) {
	if state.AnalyzedChatIDs == nil {
		state.AnalyzedChatIDs = []string{}
	}
	if state.UsageStats == nil {
		state.UsageStats = map[string]int64{}
	}
}
