package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(codebuffProvider{})
}

type codebuffProvider struct{}

func (codebuffProvider) Type() SourceType { return SourceTypeCodebuffSession }

func (codebuffProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	return discoverCodebuffSessions(env.HomeDir, env.CodebuffConfigDir, projectPath)
}

func discoverCodebuffSessions(homeDir string, configDir string, projectPath string) ([]ChatSource, error) {
	codebuffRoot := strings.TrimSpace(configDir)
	if codebuffRoot == "" {
		codebuffRoot = filepath.Join(strings.TrimSpace(homeDir), ".config", "manicode")
	}

	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}
	projectBase := filepath.Base(normalizedProjectPath)

	projectsDir := filepath.Join(codebuffRoot, "projects")
	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		return nil, nil
	}

	projectEntries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("read codebuff projects dir %q: %w", projectsDir, err)
	}

	discovered := make([]ChatSource, 0)
	for _, projectEntry := range projectEntries {
		if !projectEntry.IsDir() {
			continue
		}
		if !codebuffProjectMatches(projectEntry.Name(), projectBase) {
			continue
		}

		chatsDir := filepath.Join(projectsDir, projectEntry.Name(), "chats")
		chatEntries, err := os.ReadDir(chatsDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read codebuff chats dir %q: %w", chatsDir, err)
		}

		for _, chatEntry := range chatEntries {
			if !chatEntry.IsDir() {
				continue
			}
			messagesPath := filepath.Join(chatsDir, chatEntry.Name(), "chat-messages.json")
			info, err := os.Stat(messagesPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("stat codebuff messages %q: %w", messagesPath, err)
			}
			discovered = append(discovered, ChatSource{
				Path:         messagesPath,
				Tool:         SourceTypeCodebuffSession,
				ModifiedTime: info.ModTime(),
			})
		}
	}

	return discovered, nil
}

// codebuffProjectMatches checks whether a Codebuff project directory name
// corresponds to the target project basename. Codebuff derives project names
// from path.basename(projectRoot), so a direct case-insensitive comparison
// is sufficient.
func codebuffProjectMatches(dirName string, projectBase string) bool {
	return strings.EqualFold(strings.TrimSpace(dirName), strings.TrimSpace(projectBase))
}

func (codebuffProvider) DeleteSource(source ChatSource) error {
	return deleteSourceFile(source.Path)
}

func (codebuffProvider) SizeBytes(source ChatSource) (int64, error) {
	return statSourceSize(source.Path)
}

func (codebuffProvider) ReadMessages(source ChatSource) ([]readers.ChatMessage, error) {
	messages, err := readers.ReadCodebuffMessages(source.Path)
	if err != nil {
		return nil, fmt.Errorf("read codebuff chat source %q: %w", source.Path, err)
	}
	return messages, nil
}
