package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(kiroProvider{})
}

type kiroProvider struct{}

func (kiroProvider) Type() SourceType { return SourceTypeKiroCLISession }

func (kiroProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	return discoverKiroCLISessions(env, projectPath)
}

func discoverKiroCLISessions(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}
	if !sqliteReaderAvailable(env.KiroReader.DriverName, env.KiroReader.Open) {
		return nil, nil
	}

	dbPath := strings.TrimSpace(env.KiroCLIDBPath)
	if dbPath == "" {
		dbPath = filepath.Join(strings.TrimSpace(env.DataHomeDir), "kiro-cli", "data.sqlite3")
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat kiro database %q: %w", dbPath, err)
	}

	conversations, err := env.KiroReader.ListConversations(dbPath, "")
	if err != nil {
		return nil, fmt.Errorf("list kiro conversations: %w", err)
	}

	discovered := make([]ChatSource, 0, len(conversations))
	for _, conversation := range conversations {
		normalizedDir, ok := normalizeDiscoveryEvidencePath(conversation.Directory)
		if !ok {
			continue
		}
		if !pathWithinNormalizedRoot(normalizedDir, normalizedProjectPath) {
			continue
		}
		discovered = append(discovered, ChatSource{
			Path:         dbPath + sqliteSourcePathSeparator + conversation.ConversationID,
			Tool:         SourceTypeKiroCLISession,
			ModifiedTime: conversation.ModifiedTime,
		})
	}
	return discovered, nil
}

func (kiroProvider) DeleteSource(source ChatSource) error {
	dbPath, conversationID := SplitSQLiteSourcePath(source.Path)
	if err := readers.DeleteKiroConversation(dbPath, conversationID); err != nil {
		return fmt.Errorf("delete kiro chat source %q: %w", source.Path, err)
	}
	return nil
}

func (kiroProvider) SizeBytes(source ChatSource) (int64, error) {
	dbPath, conversationID := SplitSQLiteSourcePath(source.Path)
	return readers.KiroReader{}.ConversationSize(dbPath, conversationID)
}

func (kiroProvider) SizeBytesBatch(sources []ChatSource) map[string]int64 {
	result := make(map[string]int64, len(sources))
	if len(sources) == 0 {
		return result
	}
	byDB := make(map[string][]string, 1)
	pathByKey := make(map[string]string, len(sources))
	for _, source := range sources {
		if source.Tool != SourceTypeKiroCLISession {
			continue
		}
		dbPath, conversationID := SplitSQLiteSourcePath(source.Path)
		if dbPath == "" || conversationID == "" {
			continue
		}
		byDB[dbPath] = append(byDB[dbPath], conversationID)
		pathByKey[dbPath+"|"+conversationID] = source.Path
	}
	for dbPath, ids := range byDB {
		sizes, err := readers.KiroReader{}.ConversationSizes(dbPath, ids)
		if err != nil {
			continue
		}
		for conversationID, size := range sizes {
			if fullPath, ok := pathByKey[dbPath+"|"+conversationID]; ok {
				result[fullPath] = size
			}
		}
	}
	return result
}

func (kiroProvider) ReadMessages(source ChatSource) ([]readers.ChatMessage, error) {
	dbPath, conversationID := SplitSQLiteSourcePath(source.Path)
	messages, err := readers.ReadKiroConversation(dbPath, conversationID)
	if err != nil {
		return nil, fmt.Errorf("read kiro cli chat source %q: %w", source.Path, err)
	}
	return messages, nil
}
