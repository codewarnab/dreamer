package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(openCodeProvider{})
}

type openCodeProvider struct{}

func (openCodeProvider) Type() SourceType { return SourceTypeOpenCodeSession }

func (openCodeProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	return discoverOpenCodeSessions(env, projectPath)
}

func discoverOpenCodeSessions(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}
	if !sqliteReaderAvailable(env.OpenCodeReader.DriverName, env.OpenCodeReader.Open) {
		return nil, nil
	}

	dbPath := strings.TrimSpace(env.OpenCodeDBPath)
	if dbPath == "" {
		dbPath = filepath.Join(strings.TrimSpace(env.DataHomeDir), "opencode", "opencode.db")
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat opencode database %q: %w", dbPath, err)
	}

	sessions, err := env.OpenCodeReader.ListSessions(dbPath, "")
	if err != nil {
		return nil, fmt.Errorf("list opencode sessions: %w", err)
	}

	discovered := make([]ChatSource, 0, len(sessions))
	for _, session := range sessions {
		normalizedDir, ok := normalizeDiscoveryEvidencePath(session.Directory)
		if !ok {
			continue
		}
		if !pathWithinNormalizedRoot(normalizedDir, normalizedProjectPath) {
			continue
		}
		discovered = append(discovered, ChatSource{
			Path:         dbPath + sqliteSourcePathSeparator + session.ID,
			Tool:         SourceTypeOpenCodeSession,
			ModifiedTime: session.ModifiedTime,
		})
	}
	return discovered, nil
}

func (openCodeProvider) ReadMessages(source ChatSource) ([]readers.ChatMessage, error) {
	dbPath, sessionID := SplitSQLiteSourcePath(source.Path)
	messages, err := readers.ReadOpenCodeMessages(dbPath, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read opencode chat source %q: %w", source.Path, err)
	}
	return messages, nil
}
