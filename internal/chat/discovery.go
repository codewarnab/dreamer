package chat

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SourceType string

const (
	SourceTypeCopilotSessionJSONL SourceType = "copilot-session-jsonl"
	SourceTypeVSCodeChatSession   SourceType = "vscode-chat-session"
	SourceTypeClaudeCodeSession   SourceType = "claude-code-session-jsonl"
	SourceTypeAntigravityGemini   SourceType = "antigravity-gemini-session"
)

type ChatSource struct {
	Path         string
	Tool         SourceType
	ModifiedTime time.Time
}

func DiscoverChats(projectPath string) ([]ChatSource, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home: %w", err)
	}

	appDataDir := strings.TrimSpace(os.Getenv("APPDATA"))
	if appDataDir == "" {
		appDataDir = filepath.Join(homeDir, "AppData", "Roaming")
	}

	claudeConfigDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	geminiHomeDir := strings.TrimSpace(os.Getenv("GEMINI_HOME"))

	return discoverChatsFromRoots(homeDir, appDataDir, claudeConfigDir, projectPath, geminiHomeDir)
}

func discoverChatsFromRoots(homeDir string, appDataDir string, claudeConfigDir string, projectPath string, geminiHomeDir string) ([]ChatSource, error) {
	copilotSources, err := discoverCopilotSessionState(homeDir)
	if err != nil {
		return nil, err
	}

	vscodeSources, err := discoverVSCodeChatSessions(appDataDir)
	if err != nil {
		return nil, err
	}

	claudeSources, err := discoverClaudeCodeSessions(homeDir, claudeConfigDir)
	if err != nil {
		return nil, err
	}

	antigravitySources, err := discoverAntigravityGeminiSessions(homeDir, projectPath, geminiHomeDir)
	if err != nil {
		return nil, err
	}

	combined := append(copilotSources, vscodeSources...)
	combined = append(combined, claudeSources...)
	combined = append(combined, antigravitySources...)
	sort.Slice(combined, func(i int, j int) bool {
		left := combined[i]
		right := combined[j]
		if left.ModifiedTime.Equal(right.ModifiedTime) {
			return left.Path < right.Path
		}
		return left.ModifiedTime.After(right.ModifiedTime)
	})

	return combined, nil
}

func discoverCopilotSessionState(homeDir string) ([]ChatSource, error) {
	root := filepath.Join(strings.TrimSpace(homeDir), ".copilot", "session-state")
	return walkChatFiles(root, SourceTypeCopilotSessionJSONL, map[string]struct{}{
		".jsonl": {},
	})
}

func discoverVSCodeChatSessions(appDataDir string) ([]ChatSource, error) {
	workspaceStorageRoot := filepath.Join(strings.TrimSpace(appDataDir), "Code", "User", "workspaceStorage")
	info, err := os.Stat(workspaceStorageRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat vscode workspace storage %q: %w", workspaceStorageRoot, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	workspaceEntries, err := os.ReadDir(workspaceStorageRoot)
	if err != nil {
		return nil, fmt.Errorf("read vscode workspace storage %q: %w", workspaceStorageRoot, err)
	}

	discovered := make([]ChatSource, 0)
	for _, workspaceEntry := range workspaceEntries {
		if !workspaceEntry.IsDir() {
			continue
		}

		chatRoot := filepath.Join(workspaceStorageRoot, workspaceEntry.Name(), "chatSessions")
		workspaceChats, err := walkChatFiles(chatRoot, SourceTypeVSCodeChatSession, map[string]struct{}{
			".json":  {},
			".jsonl": {},
		})
		if err != nil {
			return nil, err
		}
		discovered = append(discovered, workspaceChats...)
	}

	return discovered, nil
}

func discoverClaudeCodeSessions(homeDir string, claudeConfigDir string) ([]ChatSource, error) {
	claudeRoot := strings.TrimSpace(claudeConfigDir)
	if claudeRoot == "" {
		claudeRoot = filepath.Join(strings.TrimSpace(homeDir), ".claude")
	}

	root := filepath.Join(claudeRoot, "projects")
	return walkChatFiles(root, SourceTypeClaudeCodeSession, map[string]struct{}{
		".jsonl": {},
	})
}

func discoverAntigravityGeminiSessions(homeDir string, projectPath string, geminiHomeDir string) ([]ChatSource, error) {
	roots := make([]string, 0, 3)

	trimmedGeminiHome := strings.TrimSpace(geminiHomeDir)
	if trimmedGeminiHome != "" {
		roots = append(roots, filepath.Join(trimmedGeminiHome, "antigravity"))
	} else {
		roots = append(roots, filepath.Join(strings.TrimSpace(homeDir), ".gemini", "antigravity"))
	}

	trimmedProjectPath := strings.TrimSpace(projectPath)
	if trimmedProjectPath != "" {
		roots = append(roots, filepath.Join(trimmedProjectPath, ".gemini", "antigravity"))
	}

	discovered := make([]ChatSource, 0)
	seen := map[string]struct{}{}
	for _, root := range roots {
		for _, conversationsDir := range []string{"conversations", "inbox"} {
			sources, err := walkChatFiles(filepath.Join(root, conversationsDir), SourceTypeAntigravityGemini, map[string]struct{}{
				".pb":    {},
				".pbtxt": {},
				".json":  {},
				".jsonl": {},
			})
			if err != nil {
				return nil, err
			}
			for _, source := range sources {
				if _, ok := seen[source.Path]; ok {
					continue
				}
				seen[source.Path] = struct{}{}
				discovered = append(discovered, source)
			}
		}
	}

	return discovered, nil
}

func walkChatFiles(root string, sourceType SourceType, extensions map[string]struct{}) ([]ChatSource, error) {
	trimmedRoot := strings.TrimSpace(root)
	if trimmedRoot == "" {
		return nil, nil
	}

	info, err := os.Stat(trimmedRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat chat root %q: %w", trimmedRoot, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	discovered := make([]ChatSource, 0)
	err = filepath.WalkDir(trimmedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}

		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if _, ok := extensions[extension]; !ok {
			return nil
		}

		fileInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("read chat file metadata for %q: %w", path, err)
		}

		discovered = append(discovered, ChatSource{
			Path:         path,
			Tool:         sourceType,
			ModifiedTime: fileInfo.ModTime().UTC(),
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk chat root %q: %w", trimmedRoot, err)
	}

	return discovered, nil
}
