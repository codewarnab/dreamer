package chat

import (
	"fmt"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(claudeProvider{})
}

type claudeProvider struct{}

func (claudeProvider) Type() SourceType { return SourceTypeClaudeCodeSession }

func (claudeProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	return discoverClaudeCodeSessions(env.HomeDir, env.ClaudeConfigDir, projectPath)
}

func discoverClaudeCodeSessions(homeDir string, claudeConfigDir string, projectPath string) ([]ChatSource, error) {
	claudeRoot := strings.TrimSpace(claudeConfigDir)
	if claudeRoot == "" {
		claudeRoot = filepath.Join(strings.TrimSpace(homeDir), ".claude")
	}

	root := filepath.Join(claudeRoot, "projects")
	candidates, err := walkChatFiles(root, SourceTypeClaudeCodeSession, map[string]struct{}{
		".jsonl": {},
	})
	if err != nil {
		return nil, err
	}

	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}

	discovered := make([]ChatSource, 0, len(candidates))
	for _, candidate := range candidates {
		candidateCWD, ok := probeClaudeSessionCWD(candidate.Path)
		if !ok {
			continue
		}
		normalizedCandidateCWD, ok := normalizeDiscoveryEvidencePath(candidateCWD)
		if !ok {
			continue
		}
		if pathWithinNormalizedRoot(normalizedCandidateCWD, normalizedProjectPath) {
			candidate.ParentID = extractClaudeParentID(candidate.Path)
			discovered = append(discovered, candidate)
		}
	}

	return discovered, nil
}

// extractClaudeParentID returns the parent session ID for a Claude Code
// subagent transcript. Paths like
//
//	<root>/<sessionId>/subagents/agent-<agentId>.jsonl
//
// yield ParentID = <sessionId>. Top-level sessions return "".
func extractClaudeParentID(path string) string {
	sep := string(filepath.Separator)
	parts := strings.Split(path, sep)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "subagents" && i > 0 {
			return parts[i-1]
		}
	}
	return ""
}

func (claudeProvider) ReadMessages(source ChatSource) ([]readers.ChatMessage, error) {
	messages, err := readers.ReadClaudeJSONLWithToolFolding(source.Path)
	if err != nil {
		return nil, fmt.Errorf("read jsonl chat source %q: %w", source.Path, err)
	}
	return readers.SanitizeClaudeMessages(messages), nil
}

func probeClaudeSessionCWD(sessionPath string) (string, bool) {
	return probeJSONLForCWD(sessionPath, probeLineLimit, func(record map[string]any) string {
		return recursiveExtract(record, claudeCWDEvidenceKeys, 8)
	})
}
