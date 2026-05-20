package chat

import (
	"fmt"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(copilotProvider{})
}

type copilotProvider struct{}

func (copilotProvider) Type() SourceType { return SourceTypeCopilotSessionJSONL }

func (copilotProvider) Discover(env DiscoveryEnvironment, _ string) ([]ChatSource, error) {
	copilotHome := strings.TrimSpace(env.CopilotHome)
	if copilotHome == "" {
		copilotHome = env.HomeDir
	}
	return discoverCopilotSessionState(copilotHome)
}

func discoverCopilotSessionState(homeDir string) ([]ChatSource, error) {
	root := filepath.Join(strings.TrimSpace(homeDir), ".copilot", "session-state")
	return walkChatFiles(root, SourceTypeCopilotSessionJSONL, map[string]struct{}{
		".jsonl": {},
	}, skipDreamerMarkedFiles)
}

func (copilotProvider) ReadMessages(source ChatSource) ([]readers.ChatMessage, error) {
	messages, err := readers.ReadJSONLWithOptions(source.Path, readers.JSONLReadOptions{
		SanitizeCopilotSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("read jsonl chat source %q: %w", source.Path, err)
	}
	return messages, nil
}
