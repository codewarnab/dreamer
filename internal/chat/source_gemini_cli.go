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
	}, skipDreamerMarkedFiles)
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
			candidate.ParentID = extractGeminiCLIParentID(candidate.Path)
			discovered = append(discovered, candidate)
		}
	}
	return discovered, nil
}

// extractGeminiCLIParentID returns the parent session ID for a Gemini CLI
// subagent transcript. Paths like
//
//	<root>/<slug>/chats/<parentId>/<childId>.jsonl
//
// yield ParentID = <parentId>. Top-level sessions directly under chats/ return "".
// The <slug> before chats/ is a project folder, not a parent session.
func extractGeminiCLIParentID(path string) string {
	sep := string(filepath.Separator)
	parts := strings.Split(path, sep)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "chats" {
			// There must be at least 2 more segments after "chats":
			// <parentId>/<filename>. If only 1, this is a top-level session.
			if i+2 < len(parts) {
				return parts[i+1]
			}
			return ""
		}
	}
	return ""
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
