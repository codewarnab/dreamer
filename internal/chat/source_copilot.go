package chat

import (
	"fmt"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(copilotProvider{fileBackedProvider{sourceType: SourceTypeCopilotSessionJSONL}})
}

type copilotProvider struct {
	fileBackedProvider
}

func (copilotProvider) Discover(env DiscoveryEnvironment, _ string) ([]Source, error) {
	copilotHome := strings.TrimSpace(env.CopilotHome)
	if copilotHome == "" {
		copilotHome = env.HomeDir
	}
	return discoverCopilotSessionState(copilotHome)
}

func discoverCopilotSessionState(homeDir string) ([]Source, error) {
	root := filepath.Join(strings.TrimSpace(homeDir), ".copilot", "session-state")
	return walkChatFiles(root, SourceTypeCopilotSessionJSONL, map[string]struct{}{
		".jsonl": {},
	}, skipDreamerMarkedFiles)
}

func (copilotProvider) ReadMessages(source Source) ([]readers.ChatMessage, error) {
	result, err := copilotProvider{}.ReadMessagesWithOptions(source, ReadOptions{})
	return result.Messages, err
}

func (copilotProvider) ReadMessagesWithOptions(source Source, options ReadOptions) (ReadResult, error) {
	result, err := readers.ReadJSONLWithOptionsResult(source.Path, readers.JSONLReadOptions{
		Sanitizer: readers.SanitizeCopilotSessionMessages,
		Budget:    options.Budget,
	})
	if err != nil {
		return ReadResult{}, fmt.Errorf("read jsonl chat source %q: %w", source.Path, err)
	}
	return ReadResult{Messages: result.Messages, Truncated: result.Truncated, Bytes: result.Bytes}, nil
}
