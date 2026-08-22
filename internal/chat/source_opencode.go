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

func (openCodeProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]Source, error) {
	return discoverOpenCodeSessions(env, projectPath)
}

func discoverOpenCodeSessions(env DiscoveryEnvironment, projectPath string) ([]Source, error) {
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

	discovered := make([]Source, 0, len(sessions))
	for _, session := range sessions {
		normalizedDir, ok := normalizeDiscoveryEvidencePath(session.Directory)
		if !ok {
			continue
		}
		if !pathWithinNormalizedRoot(normalizedDir, normalizedProjectPath) {
			continue
		}
		discovered = append(discovered, Source{
			Path:         dbPath + sqliteSourcePathSeparator + session.ID,
			Tool:         SourceTypeOpenCodeSession,
			ModifiedTime: session.ModifiedTime,
			ParentID:     session.ParentID,
		})
	}
	return discovered, nil
}

func (openCodeProvider) DeleteSource(source Source) error {
	dbPath, sessionID := SplitSQLiteSourcePath(source.Path)
	if err := readers.DeleteOpenCodeSession(dbPath, sessionID); err != nil {
		return fmt.Errorf("delete opencode chat source %q: %w", source.Path, err)
	}
	return nil
}

func (openCodeProvider) SizeBytes(source Source) (int64, error) {
	dbPath, sessionID := SplitSQLiteSourcePath(source.Path)
	return readers.OpenCodeReader{}.SessionSize(dbPath, sessionID)
}

// SourceHash returns the per-session content digest for the incremental
// cache. The shared database file is never hashed directly.
func (openCodeProvider) SourceHash(source Source) (string, error) {
	dbPath, sessionID := SplitSQLiteSourcePath(source.Path)
	fingerprint, err := readers.OpenCodeReader{}.SessionFingerprint(dbPath, sessionID)
	if err != nil {
		return "", fmt.Errorf("hash opencode chat source %q: %w", source.Path, err)
	}
	return fingerprint, nil
}

// SizeBytesBatch groups sources by underlying database path and issues one
// query per DB instead of one per session. Errors per DB are swallowed so a
// single broken file does not blank out the entire chats list.
func (openCodeProvider) SizeBytesBatch(sources []Source) map[string]int64 {
	type sessionKey struct {
		dbPath string
		id     string
	}
	result := make(map[string]int64, len(sources))
	if len(sources) == 0 {
		return result
	}
	byDB := make(map[string][]string, 1)
	pathByKey := make(map[sessionKey]string, len(sources))
	for _, source := range sources {
		if source.Tool != SourceTypeOpenCodeSession {
			continue
		}
		dbPath, sessionID := SplitSQLiteSourcePath(source.Path)
		if dbPath == "" || sessionID == "" {
			continue
		}
		byDB[dbPath] = append(byDB[dbPath], sessionID)
		pathByKey[sessionKey{dbPath, sessionID}] = source.Path
	}
	for dbPath, ids := range byDB {
		sizes, err := readers.OpenCodeReader{}.SessionSizes(dbPath, ids)
		if err != nil {
			continue
		}
		for sessionID, size := range sizes {
			if fullPath, ok := pathByKey[sessionKey{dbPath, sessionID}]; ok {
				result[fullPath] = size
			}
		}
	}
	return result
}

func (openCodeProvider) ReadMessages(source Source) ([]readers.ChatMessage, error) {
	dbPath, sessionID := SplitSQLiteSourcePath(source.Path)
	messages, err := readers.ReadOpenCodeMessages(dbPath, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read opencode chat source %q: %w", source.Path, err)
	}
	return messages, nil
}
