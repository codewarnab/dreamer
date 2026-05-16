package chat

import (
	"fmt"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(geminiCLIProvider{})
}

type geminiCLIProvider struct{}

func (geminiCLIProvider) Type() SourceType { return SourceTypeGeminiCLISession }

func (geminiCLIProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	return discoverGeminiCLISessions(env.HomeDir, env.GeminiHomeDir, projectPath)
}

func discoverGeminiCLISessions(homeDir string, geminiHomeDir string, projectPath string) ([]ChatSource, error) {
	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}

	geminiRoot := strings.TrimSpace(geminiHomeDir)
	if geminiRoot == "" {
		geminiRoot = filepath.Join(strings.TrimSpace(homeDir), ".gemini")
	}

	root := filepath.Join(geminiRoot, "tmp")
	candidates, err := walkChatFiles(root, SourceTypeGeminiCLISession, map[string]struct{}{
		".jsonl": {},
		".json":  {},
	})
	if err != nil {
		return nil, err
	}

	discovered := make([]ChatSource, 0, len(candidates))
	for _, candidate := range candidates {
		if !strings.Contains(candidate.Path, string(filepath.Separator)+"chats"+string(filepath.Separator)) {
			continue
		}
		candidateCWD, ok := probeGeminiCLISessionCWD(candidate.Path)
		if !ok {
			continue
		}
		normalizedCandidateCWD, ok := normalizeDiscoveryEvidencePath(candidateCWD)
		if !ok {
			continue
		}
		if pathWithinNormalizedRoot(normalizedCandidateCWD, normalizedProjectPath) {
			discovered = append(discovered, candidate)
		}
	}
	return discovered, nil
}

func (geminiCLIProvider) ReadMessages(source ChatSource) ([]readers.ChatMessage, error) {
	messages, err := readers.ReadGeminiCLI(source.Path)
	if err != nil {
		return nil, fmt.Errorf("read gemini cli chat source %q: %w", source.Path, err)
	}
	return messages, nil
}

func probeGeminiCLISessionCWD(sessionPath string) (string, bool) {
	return probeJSONLForCWD(sessionPath, probeLineLimit, extractGeminiCLICWD)
}

func extractGeminiCLICWD(record map[string]any) string {
	if directories, ok := record["directories"].([]any); ok {
		for _, value := range directories {
			if path := extractPathValue(value, 0); path != "" {
				return path
			}
		}
	}
	return recursiveExtract(record, claudeCWDEvidenceKeys, 8)
}
